import express from 'express';
import { Request, Response } from 'express';
import * as fs from 'fs';
import * as path from 'path';

const app = express();
const port = 8080;

// Enable JSON parsing
app.use(express.json());

// Data file path
const DATA_FILE = '/tmp/crm_data.json';

// Interface definitions
interface Customer {
    id: number;
    name: string;
    email: string;
    status: 'active' | 'inactive';
    createdAt: string;
    updatedAt: string;
    phone?: string;
    company?: string;
    tags?: string[];
}

interface Analytics {
    totalCustomers: number;
    activeCustomers: number;
    inactiveCustomers: number;
    newCustomersThisMonth: number;
    lastUpdated: string;
    averageCustomerAge: number;
}

interface CRMData {
    customers: Customer[];
    analytics: Analytics;
    lastCustomerId: number;
}

// Initialize data store
let crmData: CRMData = {
    customers: [
        { 
            id: 1, 
            name: 'John Doe', 
            email: 'john@example.com', 
            status: 'active',
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
            phone: '+1-555-0101',
            company: 'Tech Corp',
            tags: ['vip', 'enterprise']
        },
        { 
            id: 2, 
            name: 'Jane Smith', 
            email: 'jane@example.com', 
            status: 'active',
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
            phone: '+1-555-0102',
            company: 'Startup Inc',
            tags: ['startup']
        },
        { 
            id: 3, 
            name: 'Bob Johnson', 
            email: 'bob@example.com', 
            status: 'inactive',
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
            phone: '+1-555-0103',
            tags: ['former']
        }
    ],
    analytics: {
    totalCustomers: 3,
    activeCustomers: 2,
    inactiveCustomers: 1,
        newCustomersThisMonth: 0,
        lastUpdated: new Date().toISOString(),
        averageCustomerAge: 0
    },
    lastCustomerId: 3
};

// Data persistence functions
function saveData(): void {
    try {
        fs.writeFileSync(DATA_FILE, JSON.stringify(crmData, null, 2));
    } catch (error) {
        console.error('Failed to save data:', error);
    }
}

function loadData(): void {
    try {
        if (fs.existsSync(DATA_FILE)) {
            const data = fs.readFileSync(DATA_FILE, 'utf8');
            crmData = JSON.parse(data);
            updateAnalytics();
        }
    } catch (error) {
        console.error('Failed to load data:', error);
    }
}

function updateAnalytics(): void {
    const now = new Date();
    const thisMonth = now.getMonth();
    const thisYear = now.getFullYear();
    
    crmData.analytics.totalCustomers = crmData.customers.length;
    crmData.analytics.activeCustomers = crmData.customers.filter(c => c.status === 'active').length;
    crmData.analytics.inactiveCustomers = crmData.customers.filter(c => c.status === 'inactive').length;
    crmData.analytics.lastUpdated = now.toISOString();
    
    // Calculate new customers this month
    crmData.analytics.newCustomersThisMonth = crmData.customers.filter(c => {
        const created = new Date(c.createdAt);
        return created.getMonth() === thisMonth && created.getFullYear() === thisYear;
    }).length;
    
    // Calculate average customer age (days since creation)
    const totalAge = crmData.customers.reduce((sum, customer) => {
        const created = new Date(customer.createdAt);
        const ageInDays = Math.floor((now.getTime() - created.getTime()) / (1000 * 60 * 60 * 24));
        return sum + ageInDays;
    }, 0);
    
    crmData.analytics.averageCustomerAge = crmData.customers.length > 0 
        ? Math.round(totalAge / crmData.customers.length) 
        : 0;
}

// Load data on startup
loadData();

// Health check endpoint
app.get('/health', (req: Request, res: Response) => {
    res.json({
        status: 'healthy',
        timestamp: new Date().toISOString(),
        plugin: 'typescript-crm-plugin',
        version: '2.0.0',
        uptime: process.uptime(),
        memory: process.memoryUsage(),
        dataFile: fs.existsSync(DATA_FILE) ? 'exists' : 'missing'
    });
});

// Main plugin info
app.get('/', (req: Request, res: Response) => {
    res.json({
        name: 'TypeScript CRM Plugin v2',
        version: '2.0.0',
        description: 'A production-ready TypeScript CRM plugin with customer management',
        endpoints: [
            'GET  /health',
            'GET  /',
            'GET  /customers',
            'GET  /customers/:id',
            'POST /customers',
            'PUT  /customers/:id',
            'DELETE /customers/:id',
            'GET  /analytics',
            'POST /execute'
        ],
        features: [
            'Data persistence',
            'Advanced analytics',
            'Customer tagging',
            'Company tracking',
            'Phone number support'
        ]
    });
});

// Get all customers with optional filtering
app.get('/customers', (req: Request, res: Response) => {
    const { status, company, tag } = req.query;
    let filteredCustomers = [...crmData.customers];
    
    if (status) {
        filteredCustomers = filteredCustomers.filter(c => c.status === status);
    }
    
    if (company) {
        filteredCustomers = filteredCustomers.filter(c => c.company?.toLowerCase().includes(company.toString().toLowerCase()));
    }
    
    if (tag) {
        filteredCustomers = filteredCustomers.filter(c => c.tags?.includes(tag.toString()));
    }
    
    res.json({
        success: true,
        data: filteredCustomers,
        count: filteredCustomers.length,
        total: crmData.customers.length,
        timestamp: new Date().toISOString()
    });
});

// Get customer by ID
app.get('/customers/:id', (req: Request, res: Response) => {
    const id = parseInt(req.params.id);
    const customer = crmData.customers.find(c => c.id === id);
    
    if (!customer) {
        return res.status(404).json({
            success: false,
            error: 'Customer not found',
            id: id
        });
    }
    
    res.json({
        success: true,
        data: customer,
        timestamp: new Date().toISOString()
    });
});

// Add new customer
app.post('/customers', (req: Request, res: Response) => {
    const { name, email, status = 'active', phone, company, tags } = req.body;
    
    if (!name || !email) {
        return res.status(400).json({
            success: false,
            error: 'Name and email are required'
        });
    }
    
    // Check if email already exists
    if (crmData.customers.find(c => c.email === email)) {
        return res.status(409).json({
            success: false,
            error: 'Customer with this email already exists'
        });
    }
    
    const now = new Date().toISOString();
    const newCustomer: Customer = {
        id: ++crmData.lastCustomerId,
        name,
        email,
        status,
        createdAt: now,
        updatedAt: now,
        phone,
        company,
        tags: tags || []
    };
    
    crmData.customers.push(newCustomer);
    updateAnalytics();
    saveData();
    
    res.status(201).json({
        success: true,
        data: newCustomer,
        message: 'Customer created successfully',
        timestamp: now
    });
});

// Update customer
app.put('/customers/:id', (req: Request, res: Response) => {
    const id = parseInt(req.params.id);
    const customerIndex = crmData.customers.findIndex(c => c.id === id);
    
    if (customerIndex === -1) {
        return res.status(404).json({
            success: false,
            error: 'Customer not found',
            id: id
        });
    }
    
    const { name, email, status, phone, company, tags } = req.body;
    const updatedCustomer = {
        ...crmData.customers[customerIndex],
        ...(name && { name }),
        ...(email && { email }),
        ...(status && { status }),
        ...(phone !== undefined && { phone }),
        ...(company !== undefined && { company }),
        ...(tags !== undefined && { tags }),
        updatedAt: new Date().toISOString()
    };
    
    crmData.customers[customerIndex] = updatedCustomer;
    updateAnalytics();
    saveData();
    
    res.json({
        success: true,
        data: updatedCustomer,
        message: 'Customer updated successfully',
        timestamp: new Date().toISOString()
    });
});

// Delete customer
app.delete('/customers/:id', (req: Request, res: Response) => {
    const id = parseInt(req.params.id);
    const customerIndex = crmData.customers.findIndex(c => c.id === id);
    
    if (customerIndex === -1) {
        return res.status(404).json({
            success: false,
            error: 'Customer not found',
            id: id
        });
    }
    
    const deletedCustomer = crmData.customers.splice(customerIndex, 1)[0];
    updateAnalytics();
    saveData();
    
    res.json({
        success: true,
        data: deletedCustomer,
        message: 'Customer deleted successfully',
        timestamp: new Date().toISOString()
    });
});

// Get analytics
app.get('/analytics', (req: Request, res: Response) => {
    res.json({
        success: true,
        data: crmData.analytics,
        timestamp: new Date().toISOString()
    });
});

// Execute plugin with specific action
app.post('/execute', (req: Request, res: Response) => {
    const { action, data } = req.body;
    
    console.log(`Plugin executing action: ${action}`, data);
    
    try {
    switch (action) {
        case 'get_customers':
            res.json({
                success: true,
                action: 'get_customers',
                    data: crmData.customers,
                    count: crmData.customers.length,
                timestamp: new Date().toISOString()
            });
            break;
            
        case 'add_customer':
            if (!data || !data.name || !data.email) {
                return res.status(400).json({
                    success: false,
                    error: 'Name and email are required for add_customer action'
                });
            }
            
                // Check if email already exists
                if (crmData.customers.find(c => c.email === data.email)) {
                    return res.status(409).json({
                        success: false,
                        error: 'Customer with this email already exists'
                    });
                }
                
                const now = new Date().toISOString();
                const newCustomer: Customer = {
                    id: ++crmData.lastCustomerId,
                name: data.name,
                email: data.email,
                    status: data.status || 'active',
                    createdAt: now,
                    updatedAt: now,
                    phone: data.phone,
                    company: data.company,
                    tags: data.tags || []
                };
                
                crmData.customers.push(newCustomer);
                updateAnalytics();
                saveData();
            
            res.json({
                success: true,
                action: 'add_customer',
                data: newCustomer,
                message: 'Customer added successfully',
                    timestamp: now
            });
            break;
            
        case 'get_analytics':
            res.json({
                success: true,
                action: 'get_analytics',
                    data: crmData.analytics,
                    timestamp: new Date().toISOString()
                });
                break;
                
            case 'search_customers':
                const { query, status, company } = data || {};
                let searchResults = [...crmData.customers];
                
                if (query) {
                    const searchTerm = query.toLowerCase();
                    searchResults = searchResults.filter(c => 
                        c.name.toLowerCase().includes(searchTerm) ||
                        c.email.toLowerCase().includes(searchTerm) ||
                        c.company?.toLowerCase().includes(searchTerm)
                    );
                }
                
                if (status) {
                    searchResults = searchResults.filter(c => c.status === status);
                }
                
                if (company) {
                    searchResults = searchResults.filter(c => 
                        c.company?.toLowerCase().includes(company.toLowerCase())
                    );
                }
                
                res.json({
                    success: true,
                    action: 'search_customers',
                    data: searchResults,
                    count: searchResults.length,
                    query: data,
                    timestamp: new Date().toISOString()
                });
                break;
                
            case 'get_customer_by_id':
                const customerId = parseInt(data?.id);
                const customer = crmData.customers.find(c => c.id === customerId);
                
                if (!customer) {
                    return res.status(404).json({
                        success: false,
                        action: 'get_customer_by_id',
                        error: 'Customer not found',
                        id: customerId
                    });
                }
                
                res.json({
                    success: true,
                    action: 'get_customer_by_id',
                    data: customer,
                    timestamp: new Date().toISOString()
                });
                break;
                
            case 'update_customer':
                const updateId = parseInt(data?.id);
                const updateIndex = crmData.customers.findIndex(c => c.id === updateId);
                
                if (updateIndex === -1) {
                    return res.status(404).json({
                        success: false,
                        action: 'update_customer',
                        error: 'Customer not found',
                        id: updateId
                    });
                }
                
                const updatedCustomer = {
                    ...crmData.customers[updateIndex],
                    ...(data.name && { name: data.name }),
                    ...(data.email && { email: data.email }),
                    ...(data.status && { status: data.status }),
                    ...(data.phone !== undefined && { phone: data.phone }),
                    ...(data.company !== undefined && { company: data.company }),
                    ...(data.tags !== undefined && { tags: data.tags }),
                    updatedAt: new Date().toISOString()
                };
                
                crmData.customers[updateIndex] = updatedCustomer;
                updateAnalytics();
                saveData();
                
                res.json({
                    success: true,
                    action: 'update_customer',
                    data: updatedCustomer,
                    message: 'Customer updated successfully',
                timestamp: new Date().toISOString()
            });
            break;
            
        default:
            res.status(400).json({
                success: false,
                error: `Unknown action: ${action}`,
                    availableActions: [
                        'get_customers', 
                        'add_customer', 
                        'get_analytics',
                        'search_customers',
                        'get_customer_by_id',
                        'update_customer'
                    ]
                });
        }
    } catch (error) {
        console.error('Error executing action:', error);
        res.status(500).json({
            success: false,
            error: 'Internal server error',
            action: action
            });
    }
});

// Start the server
app.listen(port, '0.0.0.0', () => {
    console.log(`TypeScript CRM Plugin v2 running on port ${port}`);
    console.log(`Data persistence: ${DATA_FILE}`);
    console.log(`Available endpoints:`);
    console.log(`  GET  /health`);
    console.log(`  GET  /`);
    console.log(`  GET  /customers`);
    console.log(`  GET  /customers/:id`);
    console.log(`  POST /customers`);
    console.log(`  PUT  /customers/:id`);
    console.log(`  DELETE /customers/:id`);
    console.log(`  GET  /analytics`);
    console.log(`  POST /execute`);
}); 