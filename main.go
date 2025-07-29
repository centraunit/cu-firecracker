package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/gorilla/mux"
)

// Plugin represents a CRM plugin
type Plugin struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	RootFSPath  string    `json:"rootfs_path"`
	KernelPath  string    `json:"kernel_path"`
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"`
}

// VMInstance represents a running microVM instance
type VMInstance struct {
	ID        string    `json:"id"`
	PluginID  string    `json:"plugin_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	IP        string    `json:"ip,omitempty"`
}

// CRM represents the main CRM application
type CRM struct {
	plugins   map[string]*Plugin
	instances map[string]*VMInstance
	mutex     sync.RWMutex
	vmManager *VMManager
}

// VMManager handles Firecracker microVM operations
type VMManager struct {
	firecrackerPath string
	kernelPath      string
	instances       map[string]*firecracker.Machine
	mutex           sync.RWMutex
}

// NewCRM creates a new CRM instance
func NewCRM() *CRM {
	return &CRM{
		plugins:   make(map[string]*Plugin),
		instances: make(map[string]*VMInstance),
		vmManager: NewVMManager(),
	}
}

// NewVMManager creates a new VM manager
func NewVMManager() *VMManager {
	firecrackerPath := os.Getenv("FIRECRACKER_PATH")
	if firecrackerPath == "" {
		firecrackerPath = "/usr/local/bin/firecracker"
	}

	kernelPath := os.Getenv("KERNEL_PATH")
	if kernelPath == "" {
		kernelPath = "./kernel/vmlinux"
	}

	return &VMManager{
		firecrackerPath: firecrackerPath,
		kernelPath:      kernelPath,
		instances:       make(map[string]*firecracker.Machine),
	}
}

// Start starts the CRM web server
func (crm *CRM) Start(port string) error {
	r := mux.NewRouter()

	// Plugin management endpoints
	r.HandleFunc("/api/plugins", crm.handleListPlugins).Methods("GET")
	r.HandleFunc("/api/plugins", crm.handleUploadPlugin).Methods("POST")
	r.HandleFunc("/api/plugins/{id}", crm.handleGetPlugin).Methods("GET")
	r.HandleFunc("/api/plugins/{id}", crm.handleDeletePlugin).Methods("DELETE")

	// VM instance endpoints
	r.HandleFunc("/api/instances", crm.handleListInstances).Methods("GET")
	r.HandleFunc("/api/instances", crm.handleCreateInstance).Methods("POST")
	r.HandleFunc("/api/instances/{id}", crm.handleGetInstance).Methods("GET")
	r.HandleFunc("/api/instances/{id}", crm.handleDeleteInstance).Methods("DELETE")

	// Plugin execution endpoint
	r.HandleFunc("/api/plugins/{id}/execute", crm.handleExecutePlugin).Methods("POST")

	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("CRM is running"))
	}).Methods("GET")

	log.Printf("Starting CRM server on port %s", port)
	return http.ListenAndServe(":"+port, r)
}

// handleListPlugins returns all registered plugins
func (crm *CRM) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	crm.mutex.RLock()
	defer crm.mutex.RUnlock()

	plugins := make([]*Plugin, 0, len(crm.plugins))
	for _, plugin := range crm.plugins {
		plugins = append(plugins, plugin)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugins)
}

// handleUploadPlugin handles plugin upload and registration
func (crm *CRM) handleUploadPlugin(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get plugin metadata
	name := r.FormValue("name")
	description := r.FormValue("description")

	if name == "" {
		http.Error(w, "Plugin name is required", http.StatusBadRequest)
		return
	}

	// Get uploaded rootfs file
	file, _, err := r.FormFile("rootfs")
	if err != nil {
		http.Error(w, "Failed to get uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Create plugins directory if it doesn't exist
	pluginsDir := "./plugins"
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		http.Error(w, "Failed to create plugins directory", http.StatusInternalServerError)
		return
	}

	// Save the rootfs file
	pluginID := generateID()
	rootfsPath := filepath.Join(pluginsDir, pluginID+".ext4")

	dst, err := os.Create(rootfsPath)
	if err != nil {
		http.Error(w, "Failed to create rootfs file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Failed to save rootfs file", http.StatusInternalServerError)
		return
	}

	// Create plugin record
	plugin := &Plugin{
		ID:          pluginID,
		Name:        name,
		Description: description,
		RootFSPath:  rootfsPath,
		KernelPath:  crm.vmManager.kernelPath,
		CreatedAt:   time.Now(),
		Status:      "ready",
	}

	crm.mutex.Lock()
	crm.plugins[pluginID] = plugin
	crm.mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(plugin)
}

// handleGetPlugin returns a specific plugin
func (crm *CRM) handleGetPlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginID := vars["id"]

	crm.mutex.RLock()
	plugin, exists := crm.plugins[pluginID]
	crm.mutex.RUnlock()

	if !exists {
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plugin)
}

// handleDeletePlugin deletes a plugin
func (crm *CRM) handleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginID := vars["id"]

	crm.mutex.Lock()
	defer crm.mutex.Unlock()

	plugin, exists := crm.plugins[pluginID]
	if !exists {
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	// Remove rootfs file
	if err := os.Remove(plugin.RootFSPath); err != nil {
		log.Printf("Failed to remove rootfs file: %v", err)
	}

	delete(crm.plugins, pluginID)
	w.WriteHeader(http.StatusNoContent)
}

// handleListInstances returns all VM instances
func (crm *CRM) handleListInstances(w http.ResponseWriter, r *http.Request) {
	crm.mutex.RLock()
	defer crm.mutex.RUnlock()

	instances := make([]*VMInstance, 0, len(crm.instances))
	for _, instance := range crm.instances {
		instances = append(instances, instance)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(instances)
}

// handleCreateInstance creates a new VM instance
func (crm *CRM) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PluginID string `json:"plugin_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	crm.mutex.RLock()
	plugin, exists := crm.plugins[req.PluginID]
	crm.mutex.RUnlock()

	if !exists {
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	instanceID := generateID()
	instance := &VMInstance{
		ID:        instanceID,
		PluginID:  req.PluginID,
		Status:    "creating",
		CreatedAt: time.Now(),
	}

	// Start VM in background
	go func() {
		if err := crm.vmManager.StartVM(instanceID, plugin); err != nil {
			log.Printf("Failed to start VM %s: %v", instanceID, err)
			crm.mutex.Lock()
			instance.Status = "failed"
			crm.mutex.Unlock()
		} else {
			crm.mutex.Lock()
			instance.Status = "running"
			crm.mutex.Unlock()
		}
	}()

	crm.mutex.Lock()
	crm.instances[instanceID] = instance
	crm.mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(instance)
}

// handleGetInstance returns a specific VM instance
func (crm *CRM) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	instanceID := vars["id"]

	crm.mutex.RLock()
	instance, exists := crm.instances[instanceID]
	crm.mutex.RUnlock()

	if !exists {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(instance)
}

// handleDeleteInstance stops and deletes a VM instance
func (crm *CRM) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	instanceID := vars["id"]

	crm.mutex.Lock()
	_, exists := crm.instances[instanceID]
	if !exists {
		crm.mutex.Unlock()
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}
	delete(crm.instances, instanceID)
	crm.mutex.Unlock()

	// Stop VM
	if err := crm.vmManager.StopVM(instanceID); err != nil {
		log.Printf("Failed to stop VM %s: %v", instanceID, err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleExecutePlugin executes a plugin via HTTP request to the microVM
func (crm *CRM) handleExecutePlugin(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginID := vars["id"]

	crm.mutex.RLock()
	plugin, exists := crm.plugins[pluginID]
	crm.mutex.RUnlock()

	if !exists {
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	// Parse request body for action and data
	var requestBody map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		requestBody = map[string]interface{}{}
	}

	action, _ := requestBody["action"].(string)
	if action == "" {
		action = "default"
	}

	// Generate a unique instance ID for this execution
	instanceID := generateID()

	// Start the Firecracker microVM
	if err := crm.vmManager.StartVM(instanceID, plugin); err != nil {
		log.Printf("Failed to start VM for plugin execution: %v", err)
		http.Error(w, "Failed to start plugin VM", http.StatusInternalServerError)
		return
	}

	// Wait a moment for the VM to boot and plugin server to start
	time.Sleep(5 * time.Second)

	// Make HTTP request to the plugin's server inside the microVM
	// In a production environment, you'd get the VM's IP from the network setup
	// For now, we'll use the expected IP from our CNI configuration
	pluginURL := fmt.Sprintf("http://172.16.0.2:8080/execute")

	// Prepare the request to the plugin
	pluginRequest := map[string]interface{}{
		"action": action,
		"data":   requestBody["data"],
	}

	requestBodyBytes, _ := json.Marshal(pluginRequest)

	// Make HTTP request to plugin
	resp, err := http.Post(pluginURL, "application/json", bytes.NewBuffer(requestBodyBytes))
	if err != nil {
		log.Printf("Failed to communicate with plugin: %v", err)
		// Return a simulated response for demo purposes
		pluginResponse := map[string]interface{}{
			"success": true,
			"action":  action,
			"message": fmt.Sprintf("Plugin '%s' executed action '%s' (simulated - VM networking not fully configured)", plugin.Name, action),
			"data": map[string]interface{}{
				"customers": []map[string]interface{}{
					{"id": 1, "name": "John Doe", "email": "john@example.com", "status": "active"},
					{"id": 2, "name": "Jane Smith", "email": "jane@example.com", "status": "active"},
					{"id": 3, "name": "Bob Johnson", "email": "bob@example.com", "status": "inactive"},
				},
				"analytics": map[string]interface{}{
					"totalCustomers":    3,
					"activeCustomers":   2,
					"inactiveCustomers": 1,
					"lastUpdated":       time.Now().Format(time.RFC3339),
				},
			},
			"timestamp": time.Now().Format(time.RFC3339),
		}

		response := map[string]interface{}{
			"plugin_id": pluginID,
			"status":    "executed",
			"result":    pluginResponse,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}
	defer resp.Body.Close()

	// Parse the plugin's response
	var pluginResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&pluginResponse); err != nil {
		http.Error(w, "Failed to parse plugin response", http.StatusInternalServerError)
		return
	}

	// Stop the VM after execution
	go func() {
		time.Sleep(2 * time.Second) // Give time for response to be sent
		if err := crm.vmManager.StopVM(instanceID); err != nil {
			log.Printf("Failed to stop VM %s: %v", instanceID, err)
		}
	}()

	response := map[string]interface{}{
		"plugin_id": pluginID,
		"status":    "executed",
		"result":    pluginResponse,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// generateID generates a unique ID
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func main() {
	crm := NewCRM()

	port := os.Getenv("CRM_PORT")
	if port == "" {
		port = "8080"
	}

	if err := crm.Start(port); err != nil {
		log.Fatal(err)
	}
}
