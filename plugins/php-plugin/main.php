<?php

// Enable error reporting for development
error_reporting(E_ALL);
ini_set('display_errors', 1);

// Set content type to JSON
header('Content-Type: application/json');

// Get request method and path
$method = $_SERVER['REQUEST_METHOD'];
$path = $_SERVER['REQUEST_URI'];
$port = $_ENV['PORT'] ?? 8080;

// Simple routing
switch ($path) {
    case '/health':
        handleHealth();
        break;
    case '/':
        handleMain();
        break;
    case '/execute':
        if ($method === 'POST') {
            handleExecute();
        } else {
            http_response_code(405);
            echo json_encode(['error' => 'Method not allowed']);
        }
        break;
    case '/api/customers':
        if ($method === 'GET') {
            handleGetCustomers();
        } elseif ($method === 'POST') {
            handleCreateCustomer();
        } else {
            http_response_code(405);
            echo json_encode(['error' => 'Method not allowed']);
        }
        break;
    case '/api/reports':
        if ($method === 'GET') {
            handleReports();
        } else {
            http_response_code(405);
            echo json_encode(['error' => 'Method not allowed']);
        }
        break;
    default:
        http_response_code(404);
        echo json_encode(['error' => 'Not found']);
        break;
}

function handleHealth() {
    echo json_encode([
        'status' => 'healthy',
        'plugin' => 'php-cms-plugin',
        'version' => '1.0.0',
        'timestamp' => date('c')
    ]);
}

function handleMain() {
    echo json_encode([
        'message' => 'Hello from PHP CMS Plugin!',
        'plugin' => 'php-cms-plugin',
        'version' => '1.0.0',
        'timestamp' => date('c'),
        'request' => [
            'method' => $_SERVER['REQUEST_METHOD'],
            'path' => $_SERVER['REQUEST_URI'],
            'headers' => getallheaders()
        ]
    ]);
}

function handleExecute() {
    $input = json_decode(file_get_contents('php://input'), true) ?: [];
    $action = $input['action'] ?? 'default';
    
    echo json_encode([
        'status' => 'success',
        'action' => $action,
        'data' => $input,
        'result' => 'PHP plugin executed successfully',
        'timestamp' => date('c')
    ]);
}

function handleGetCustomers() {
    echo json_encode([
        'customers' => [
            ['id' => 1, 'name' => 'Charlie Brown', 'email' => 'charlie@example.com'],
            ['id' => 2, 'name' => 'Diana Prince', 'email' => 'diana@example.com']
        ],
        'total' => 2,
        'plugin' => 'php-cms-plugin'
    ]);
}

function handleCreateCustomer() {
    $input = json_decode(file_get_contents('php://input'), true) ?: [];
    $name = $input['name'] ?? 'Unknown';
    $email = $input['email'] ?? 'unknown@example.com';
    
    echo json_encode([
        'status' => 'success',
        'message' => 'Customer created via PHP plugin',
        'customer' => ['id' => 3, 'name' => $name, 'email' => $email],
        'plugin' => 'php-cms-plugin'
    ]);
}

function handleReports() {
    echo json_encode([
        'reports' => [
            'sales_summary' => [
                'total_sales' => 75000,
                'monthly_growth' => 15,
                'top_products' => ['Product A', 'Product B', 'Product C']
            ],
            'customer_insights' => [
                'new_customers' => 45,
                'churn_rate' => 2.5,
                'avg_lifetime_value' => 1200
            ]
        ],
        'plugin' => 'php-cms-plugin'
    ]);
}

?> 