<?php
// PHP Content Manager Plugin
error_reporting(E_ALL);
ini_set('display_errors', 1);

class ContentManager {
    private $content = [];
    private $users = [];
    private $cache = [];
    private $dataFile = '/tmp/cms_content.json';
    
    public function __construct() {
        $this->loadData();
        $this->initDefaultData();
    }
    
    private function loadData() {
        if (file_exists($this->dataFile)) {
            $data = json_decode(file_get_contents($this->dataFile), true);
            if ($data) {
                $this->content = $data['content'] ?? [];
                $this->users = $data['users'] ?? [];
                $this->cache = $data['cache'] ?? [];
            }
        }
    }
    
    private function saveData() {
        $data = [
            'content' => $this->content,
            'users' => $this->users,
            'cache' => $this->cache,
            'last_updated' => date('c')
        ];
        file_put_contents($this->dataFile, json_encode($data, JSON_PRETTY_PRINT));
    }
    
    private function initDefaultData() {
        if (empty($this->content)) {
            $this->content = [
                ['id' => 1, 'title' => 'Welcome Post', 'content' => 'Welcome to PHP CMS!', 'status' => 'published', 'created_at' => date('c')],
                ['id' => 2, 'title' => 'About Us', 'content' => 'This is a PHP-powered content management system.', 'status' => 'draft', 'created_at' => date('c')]
            ];
        }
        
        if (empty($this->users)) {
            $this->users = [
                ['id' => 1, 'username' => 'admin', 'email' => 'admin@example.com', 'role' => 'administrator', 'created_at' => date('c')],
                ['id' => 2, 'username' => 'editor', 'email' => 'editor@example.com', 'role' => 'editor', 'created_at' => date('c')]
            ];
        }
        
        $this->saveData();
    }
    
    public function handleContentAction($data) {
        $action = $data['action'] ?? 'list';
        
        switch ($action) {
            case 'create':
                $newContent = [
                    'id' => count($this->content) + 1,
                    'title' => $data['title'] ?? 'Untitled',
                    'content' => $data['content'] ?? '',
                    'status' => $data['status'] ?? 'draft',
                    'created_at' => date('c'),
                    'updated_at' => date('c')
                ];
                $this->content[] = $newContent;
                $this->saveData();
                return ['success' => true, 'content' => $newContent];
                
            case 'list':
                return ['success' => true, 'content' => $this->content, 'total' => count($this->content)];
                
            case 'publish':
                $id = $data['id'] ?? 0;
                foreach ($this->content as &$item) {
                    if ($item['id'] == $id) {
                        $item['status'] = 'published';
                        $item['published_at'] = date('c');
                        $this->saveData();
                        return ['success' => true, 'content' => $item];
                    }
                }
                return ['success' => false, 'error' => 'Content not found'];
                
            default:
                return ['success' => false, 'error' => 'Unknown action'];
        }
    }
    
    public function handleAuthAction($data) {
        $action = $data['action'] ?? 'status';
        
        switch ($action) {
            case 'login':
                $username = $data['username'] ?? '';
                $user = null;
                foreach ($this->users as $u) {
                    if ($u['username'] === $username) {
                        $user = $u;
                        break;
                    }
                }
                
                if ($user) {
                    $sessionId = bin2hex(random_bytes(16));
                    return [
                        'success' => true,
                        'user' => $user,
                        'session_id' => $sessionId,
                        'expires_at' => date('c', strtotime('+1 hour'))
                    ];
                }
                return ['success' => false, 'error' => 'Invalid credentials'];
                
            case 'register':
                $newUser = [
                    'id' => count($this->users) + 1,
                    'username' => $data['username'] ?? 'user' . time(),
                    'email' => $data['email'] ?? '',
                    'role' => 'user',
                    'created_at' => date('c')
                ];
                $this->users[] = $newUser;
                $this->saveData();
                return ['success' => true, 'user' => $newUser];
                
            case 'list':
                return ['success' => true, 'users' => $this->users, 'total' => count($this->users)];
                
            default:
                return ['success' => false, 'error' => 'Unknown auth action'];
        }
    }
    
    public function handleCacheAction($data) {
        $action = $data['action'] ?? 'status';
        
        switch ($action) {
            case 'clear':
                $this->cache = [];
                $this->saveData();
                return ['success' => true, 'message' => 'Cache cleared', 'timestamp' => date('c')];
                
            case 'warm':
                $this->cache = [
                    'content_cache' => array_slice($this->content, 0, 5),
                    'user_cache' => array_slice($this->users, 0, 3),
                    'warmed_at' => date('c')
                ];
                $this->saveData();
                return ['success' => true, 'message' => 'Cache warmed', 'timestamp' => date('c')];
                
            case 'status':
                return [
                    'success' => true,
                    'cache_size' => count($this->cache),
                    'cache_data' => $this->cache,
                    'memory_usage' => memory_get_usage(true)
                ];
                
            default:
                return ['success' => false, 'error' => 'Unknown cache action'];
        }
    }
    
    public function getDiscovery() {
        return [
            'plugin' => 'PHP Content Manager',
            'version' => '1.0.0',
            'actions' => [
                'content' => ['create', 'list', 'publish'],
                'auth' => ['login', 'register', 'list'],
                'cache' => ['clear', 'warm', 'status']
            ],
            'endpoints' => [
                '/actions/content' => 'Content management operations',
                '/actions/auth' => 'User authentication operations',
                '/actions/cache' => 'Cache management operations',
                '/health' => 'Health check endpoint',
                '/actions' => 'Discovery endpoint'
            ],
            'timestamp' => date('c')
        ];
    }
    
    public function getHealth() {
        $health = [
            'status' => 'healthy',
            'php_version' => PHP_VERSION,
            'memory_usage' => memory_get_usage(true),
            'uptime' => time() - (int)($_SERVER['REQUEST_TIME'] ?? time()),
            'data_file' => file_exists($this->dataFile) ? 'exists' : 'missing',
            'content_count' => count($this->content),
            'user_count' => count($this->users),
            'cache_entries' => count($this->cache),
            'timestamp' => date('c')
        ];
        
        return $health;
    }
}

// Initialize the content manager
$cm = new ContentManager();

// Simple HTTP request routing
$method = $_SERVER['REQUEST_METHOD'] ?? 'GET';
$uri = $_SERVER['REQUEST_URI'] ?? '/';
$path = parse_url($uri, PHP_URL_PATH);

// Set JSON headers
header('Content-Type: application/json');
header('Access-Control-Allow-Origin: *');
header('Access-Control-Allow-Methods: GET, POST, OPTIONS');
header('Access-Control-Allow-Headers: Content-Type');

if ($method === 'OPTIONS') {
    http_response_code(200);
    exit;
}

// Get request body for POST requests
$input = [];
if ($method === 'POST') {
    $json = file_get_contents('php://input');
    if ($json) {
        $input = json_decode($json, true) ?? [];
    }
}

// Route handlers
try {
    switch ($path) {
        case '/health':
            echo json_encode($cm->getHealth());
            break;
            
        case '/actions':
            echo json_encode($cm->getDiscovery());
            break;
            
        case '/actions/content':
            echo json_encode($cm->handleContentAction($input));
            break;
            
        case '/actions/auth':
            echo json_encode($cm->handleAuthAction($input));
            break;
            
        case '/actions/cache':
            echo json_encode($cm->handleCacheAction($input));
            break;
            
        case '/':
            echo json_encode([
                'message' => 'PHP Content Manager Plugin',
                'version' => '1.0.0',
                'endpoints' => ['/health', '/actions', '/actions/content', '/actions/auth', '/actions/cache'],
                'timestamp' => date('c')
            ]);
            break;
            
        default:
            http_response_code(404);
            echo json_encode(['error' => 'Endpoint not found', 'path' => $path]);
            break;
    }
} catch (Exception $e) {
    http_response_code(500);
    echo json_encode([
        'error' => 'Internal server error',
        'message' => $e->getMessage(),
        'timestamp' => date('c')
    ]);
}
?> 