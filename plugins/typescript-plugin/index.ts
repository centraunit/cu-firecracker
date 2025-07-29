import express from 'express';
import { Request, Response } from 'express';

const app = express();
const port = 8080;

// Enable JSON parsing
app.use(express.json());

// In-memory data store for demo
let customers = [
    { id: 1, name: 'John Doe', email: 'john@example.com', status: 'active' },
    { id: 2, name: 'Jane Smith', email: 'jane@example.com', status: 'active' },
    { id: 3, name: 'Bob Johnson', email: 'bob@example.com', status: 'inactive' }
];

let analytics = {
    totalCustomers: 3,
    activeCustomers: 2,
    inactiveCustomers: 1,
    lastUpdated: new Date().toISOString()
};

// Health check endpoint
app.get('/health', (req: Request, res: Response) => {
    res.json({
        status: 'healthy',
        timestamp: new Date().toISOString(),
        plugin: 'typescript-crm-plugin',
        version: '1.0.0'
    });
});

// Main plugin info
app.get('/', (req: Request, res: Response) => {
    res.json({
        name: 'TypeScript CRM Plugin',
        version: '1.0.0',
        description: 'A TypeScript CRM plugin with customer management',
        endpoints: [
            '/health',
            '/customers',
            '/customers/:id',
            '/analytics',
            '/execute'
        ]
    });
});

// Get all customers
app.get('/customers', (req: Request, res: Response) => {
    res.json({
        success: true,
        data: customers,
        count: customers.length,
        timestamp: new Date().toISOString()
    });
});

// Get customer by ID
app.get('/customers/:id', (req: Request, res: Response) => {
    const id = parseInt(req.params.id);
    const customer = customers.find(c => c.id === id);
    
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
    const { name, email, status = 'active' } = req.body;
    
    if (!name || !email) {
        return res.status(400).json({
            success: false,
            error: 'Name and email are required'
        });
    }
    
    const newCustomer = {
        id: customers.length + 1,
        name,
        email,
        status
    };
    
    customers.push(newCustomer);
    analytics.totalCustomers = customers.length;
    analytics.activeCustomers = customers.filter(c => c.status === 'active').length;
    analytics.inactiveCustomers = customers.filter(c => c.status === 'inactive').length;
    analytics.lastUpdated = new Date().toISOString();
    
    res.status(201).json({
        success: true,
        data: newCustomer,
        message: 'Customer created successfully',
        timestamp: new Date().toISOString()
    });
});

// Update customer
app.put('/customers/:id', (req: Request, res: Response) => {
    const id = parseInt(req.params.id);
    const customerIndex = customers.findIndex(c => c.id === id);
    
    if (customerIndex === -1) {
        return res.status(404).json({
            success: false,
            error: 'Customer not found',
            id: id
        });
    }
    
    const { name, email, status } = req.body;
    const updatedCustomer = {
        ...customers[customerIndex],
        ...(name && { name }),
        ...(email && { email }),
        ...(status && { status })
    };
    
    customers[customerIndex] = updatedCustomer;
    analytics.activeCustomers = customers.filter(c => c.status === 'active').length;
    analytics.inactiveCustomers = customers.filter(c => c.status === 'inactive').length;
    analytics.lastUpdated = new Date().toISOString();
    
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
    const customerIndex = customers.findIndex(c => c.id === id);
    
    if (customerIndex === -1) {
        return res.status(404).json({
            success: false,
            error: 'Customer not found',
            id: id
        });
    }
    
    const deletedCustomer = customers.splice(customerIndex, 1)[0];
    analytics.totalCustomers = customers.length;
    analytics.activeCustomers = customers.filter(c => c.status === 'active').length;
    analytics.inactiveCustomers = customers.filter(c => c.status === 'inactive').length;
    analytics.lastUpdated = new Date().toISOString();
    
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
        data: analytics,
        timestamp: new Date().toISOString()
    });
});

// Execute plugin with specific action
app.post('/execute', (req: Request, res: Response) => {
    const { action, data } = req.body;
    
    console.log(`Plugin executing action: ${action}`, data);
    
    switch (action) {
        case 'get_customers':
            res.json({
                success: true,
                action: 'get_customers',
                data: customers,
                count: customers.length,
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
            
            const newCustomer = {
                id: customers.length + 1,
                name: data.name,
                email: data.email,
                status: data.status || 'active'
            };
            
            customers.push(newCustomer);
            analytics.totalCustomers = customers.length;
            analytics.activeCustomers = customers.filter(c => c.status === 'active').length;
            analytics.inactiveCustomers = customers.filter(c => c.status === 'inactive').length;
            analytics.lastUpdated = new Date().toISOString();
            
            res.json({
                success: true,
                action: 'add_customer',
                data: newCustomer,
                message: 'Customer added successfully',
                timestamp: new Date().toISOString()
            });
            break;
            
        case 'get_analytics':
            res.json({
                success: true,
                action: 'get_analytics',
                data: analytics,
                timestamp: new Date().toISOString()
            });
            break;
            
        default:
            res.status(400).json({
                success: false,
                error: `Unknown action: ${action}`,
                availableActions: ['get_customers', 'add_customer', 'get_analytics']
            });
    }
});

// Start the server
app.listen(port, '0.0.0.0', () => {
    console.log(`TypeScript CRM Plugin running on port ${port}`);
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