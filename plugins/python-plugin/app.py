#!/usr/bin/env python3
import json
import time
import random
from datetime import datetime
from flask import Flask, request, jsonify

# Configure Flask for production
app = Flask(__name__)
app.config['ENV'] = 'production'
app.config['DEBUG'] = False
app.config['TESTING'] = False

print("=== Starting Python CMS Plugin ===")
print(f"Flask app initialized at {datetime.now()}")
print("Running in PRODUCTION mode")

# Track start time for uptime calculation
start_time = time.time()

# In-memory data for testing - completely different from TypeScript
data_store = {
    "counters": {"requests": 0, "calculations": 0, "queries": 0},
    "products": [
        {"id": 1, "name": "Python Book", "price": 29.99, "category": "books", "stock": 50},
        {"id": 2, "name": "Flask Guide", "price": 39.99, "category": "books", "stock": 25},
        {"id": 3, "name": "Code Editor", "price": 99.99, "category": "software", "stock": 100}
    ],
    "orders": [],
    "analytics": {"total_revenue": 0, "orders_count": 0}
}

@app.route('/health', methods=['GET'])
def health():
    """Ultra-fast health check"""
    data_store["counters"]["requests"] += 1
    return jsonify({
        "status": "healthy",
        "timestamp": datetime.now().isoformat(),
        "plugin_type": "python-performance",
        "version": "1.1.0",
        "requests_served": data_store["counters"]["requests"],
        "uptime_seconds": int(time.time() - start_time),
        "memory_usage": "low",
        "response_time_ms": 1,
        "new_features": ["Enhanced analytics", "Version tracking", "Performance metrics"]
    })

@app.route('/actions', methods=['GET'])
def get_actions():
    """Discovery endpoint - returns available actions"""
    return jsonify({
        "plugin_slug": "python-performance",
        "actions": {
            "product_created": {
                "name": "Product Created Handler",
                "description": "Handles product creation events",
                "hooks": ["product.created", "product.updated"],
                "method": "POST",
                "endpoint": "/actions/product",
                "priority": 10
            },
            "order_processing": {
                "name": "Order Processing Handler", 
                "description": "Handles order processing events",
                "hooks": ["order.created", "order.updated", "order.paid"],
                "method": "POST",
                "endpoint": "/actions/order",
                "priority": 5
            },
            "analytics_calculation": {
                "name": "Analytics Calculator",
                "description": "Calculates analytics and metrics",
                "hooks": ["analytics.calculate", "report.generate"],
                "method": "POST", 
                "endpoint": "/actions/analytics",
                "priority": 1
            }
        },
        "timestamp": datetime.now().isoformat()
    })

# Action endpoints
@app.route('/actions/product', methods=['POST'])
def handle_product_action():
    """Handle product-related actions"""
    start_time = time.time()
    data_store["counters"]["requests"] += 1
    
    try:
        request_data = request.get_json() or {}
        hook = request_data.get('hook', 'unknown')
        payload = request_data.get('payload', {})
        
        print(f"Processing product action: {hook}")
        
        if hook in ['product.created', 'product.updated']:
            # Simulate product processing
            product_id = payload.get('id', 'unknown')
            product_name = payload.get('name', 'Unknown Product')
            
            # Add to our products if new
            if hook == 'product.created':
                new_product = {
                    "id": len(data_store["products"]) + 1,
                    "name": product_name,
                    "price": payload.get('price', 0.0),
                    "category": payload.get('category', 'general'),
                    "stock": payload.get('stock', 0)
                }
                data_store["products"].append(new_product)
                
            processing_time = (time.time() - start_time) * 1000
            return jsonify({
                "success": True,
                "hook": hook,
                "message": f"Processed {hook} for product {product_name}",
                "processing_time_ms": round(processing_time, 2),
                "timestamp": datetime.now().isoformat()
            })
        else:
            return jsonify({
                "success": False,
                "error": f"Unsupported hook: {hook}",
                "supported_hooks": ["product.created", "product.updated"]
            }), 400
            
    except Exception as e:
        return jsonify({
            "success": False,
            "error": str(e),
            "hook": hook if 'hook' in locals() else 'unknown'
        }), 500

@app.route('/actions/order', methods=['POST'])
def handle_order_action():
    """Handle order-related actions"""
    start_time = time.time()
    data_store["counters"]["requests"] += 1
    
    try:
        request_data = request.get_json() or {}
        hook = request_data.get('hook', 'unknown')
        payload = request_data.get('payload', {})
        
        print(f"Processing order action: {hook}")
        
        if hook in ['order.created', 'order.updated', 'order.paid']:
            order_id = payload.get('id', 'unknown')
            total = payload.get('total', 0.0)
            
            if hook == 'order.created':
                new_order = {
                    "id": len(data_store["orders"]) + 1,
                    "total_price": total,
                    "created_at": datetime.now().isoformat(),
                    "status": "processing"
                }
                data_store["orders"].append(new_order)
                data_store["analytics"]["total_revenue"] += total
                data_store["analytics"]["orders_count"] += 1
                
            processing_time = (time.time() - start_time) * 1000
            return jsonify({
                "success": True,
                "hook": hook,
                "message": f"Processed {hook} for order {order_id}",
                "processing_time_ms": round(processing_time, 2),
                "timestamp": datetime.now().isoformat()
            })
        else:
            return jsonify({
                "success": False,
                "error": f"Unsupported hook: {hook}",
                "supported_hooks": ["order.created", "order.updated", "order.paid"]
            }), 400
            
    except Exception as e:
        return jsonify({
            "success": False,
            "error": str(e),
            "hook": hook if 'hook' in locals() else 'unknown'
        }), 500

@app.route('/actions/analytics', methods=['POST'])
def handle_analytics_action():
    """Handle analytics-related actions"""
    start_time = time.time()
    data_store["counters"]["calculations"] += 1
    
    try:
        request_data = request.get_json() or {}
        hook = request_data.get('hook', 'unknown')
        payload = request_data.get('payload', {})
        
        print(f"Processing analytics action: {hook}")
        
        if hook in ['analytics.calculate', 'report.generate']:
            # Perform analytics calculations
            total_products = len(data_store["products"])
            total_orders = len(data_store["orders"])
            avg_order_value = data_store["analytics"]["total_revenue"] / max(total_orders, 1)
            
            analytics_data = {
                "total_products": total_products,
                "total_orders": total_orders,
                "total_revenue": data_store["analytics"]["total_revenue"],
                "average_order_value": round(avg_order_value, 2),
                "calculations_performed": data_store["counters"]["calculations"],
                "calculated_at": datetime.now().isoformat(),
                "version": "1.1.0",
                "new_feature": "Enhanced analytics with version tracking",
                "performance_metrics": {
                    "response_time_ms": round((time.time() - start_time) * 1000, 2),
                    "memory_efficient": True,
                    "optimized": True
                }
            }
            
            processing_time = (time.time() - start_time) * 1000
            return jsonify({
                "success": True,
                "hook": hook,
                "data": analytics_data,
                "processing_time_ms": round(processing_time, 2),
                "timestamp": datetime.now().isoformat()
            })
        else:
            return jsonify({
                "success": False,
                "error": f"Unsupported hook: {hook}",
                "supported_hooks": ["analytics.calculate", "report.generate"]
            }), 400
            
    except Exception as e:
        return jsonify({
            "success": False,
            "error": str(e),
            "hook": hook if 'hook' in locals() else 'unknown'
        }), 500

@app.route('/ping', methods=['GET', 'POST'])
def ping():
    """Minimal ping endpoint"""
    return jsonify({
        "message": "pong from Python",
        "timestamp": datetime.now().isoformat(),
        "response_time_ms": 1
    })

@app.route('/benchmark', methods=['GET', 'POST'])
def benchmark():
    """Pure computation benchmark"""
    start_time = time.time()
    
    # Simple computation to test processing speed
    result = sum(i * i for i in range(1000))
    
    processing_time = (time.time() - start_time) * 1000
    
    return jsonify({
        "benchmark_result": result,
        "processing_time_ms": round(processing_time, 3),
        "timestamp": datetime.now().isoformat(),
        "computation": "sum of squares 1-1000"
    })

@app.route('/execute', methods=['POST'])
def execute():
    """Main execution endpoint with different actions"""
    start_time = time.time()
    data_store["counters"]["requests"] += 1
    
    try:
        request_data = request.get_json() or {}
        action = request_data.get('action', 'default')
        
        print(f"Processing action: {action}")
        
        if action == 'list_products':
            data_store["counters"]["queries"] += 1
            result = {
                "action": "list_products",
                "data": data_store["products"],
                "count": len(data_store["products"]),
                "success": True
            }
            
        elif action == 'create_order':
            order_data = request_data.get('data', {})
            product_id = order_data.get('product_id', 1)
            quantity = order_data.get('quantity', 1)
            
            # Find product
            product = next((p for p in data_store["products"] if p["id"] == product_id), None)
            if not product:
                result = {
                    "action": "create_order",
                    "error": "Product not found",
                    "success": False
                }
            else:
                total_price = product["price"] * quantity
                new_order = {
                    "id": len(data_store["orders"]) + 1,
                    "product_id": product_id,
                    "product_name": product["name"],
                    "quantity": quantity,
                    "unit_price": product["price"],
                    "total_price": total_price,
                    "created_at": datetime.now().isoformat(),
                    "status": "confirmed"
                }
                data_store["orders"].append(new_order)
                data_store["analytics"]["total_revenue"] += total_price
                data_store["analytics"]["orders_count"] += 1
                
                result = {
                    "action": "create_order",
                    "data": new_order,
                    "message": "Order created successfully",
                    "success": True
                }
                
        elif action == 'get_analytics':
            # Calculate some analytics
            total_products = len(data_store["products"])
            total_orders = len(data_store["orders"])
            avg_order_value = data_store["analytics"]["total_revenue"] / max(total_orders, 1)
            
            result = {
                "action": "get_analytics",
                "data": {
                    "total_products": total_products,
                    "total_orders": total_orders,
                    "total_revenue": data_store["analytics"]["total_revenue"],
                    "average_order_value": round(avg_order_value, 2),
                    "recent_orders": data_store["orders"][-5:] if data_store["orders"] else []
                },
                "success": True
            }
            
        elif action == 'calculate_fibonacci':
            # CPU-intensive task for performance testing
            data_store["counters"]["calculations"] += 1
            n = request_data.get('data', {}).get('n', 20)
            n = min(n, 35)  # Limit to prevent too long computation
            
            def fibonacci(num):
                if num <= 1:
                    return num
                return fibonacci(num-1) + fibonacci(num-2)
            
            fib_result = fibonacci(n)
            result = {
                "action": "calculate_fibonacci",
                "data": {
                    "input": n,
                    "result": fib_result,
                    "calculations_performed": data_store["counters"]["calculations"]
                },
                "success": True
            }
            
        elif action == 'simulate_delay':
            # Test network/IO simulation
            delay = request_data.get('data', {}).get('delay_ms', 100)
            delay = min(delay, 2000)  # Max 2 seconds
            time.sleep(delay / 1000.0)
            
            result = {
                "action": "simulate_delay",
                "data": {
                    "requested_delay_ms": delay,
                    "message": f"Simulated {delay}ms delay"
                },
                "success": True
            }
            
        elif action == 'random_data':
            # Generate random data
            random_numbers = [random.randint(1, 100) for _ in range(10)]
            result = {
                "action": "random_data",
                "data": {
                    "random_numbers": random_numbers,
                    "sum": sum(random_numbers),
                    "average": sum(random_numbers) / len(random_numbers),
                    "max": max(random_numbers),
                    "min": min(random_numbers)
                },
                "success": True
            }
            
        else:
            result = {
                "action": action,
                "error": f"Unknown action: {action}",
                "available_actions": [
                    "list_products", 
                    "create_order", 
                    "get_analytics", 
                    "calculate_fibonacci", 
                    "simulate_delay", 
                    "random_data"
                ],
                "success": False
            }
        
        processing_time = (time.time() - start_time) * 1000  # Convert to ms
        
        response = {
            **result,
            "timestamp": datetime.now().isoformat(),
            "processing_time_ms": round(processing_time, 2),
            "total_requests": data_store["counters"]["requests"],
            "plugin_type": "python"
        }
        
        print(f"Action {action} completed in {processing_time:.2f}ms")
        return jsonify(response)
        
    except Exception as e:
        error_response = {
            "action": request_data.get('action', 'unknown'),
            "error": str(e),
            "success": False,
            "timestamp": datetime.now().isoformat(),
            "processing_time_ms": round((time.time() - start_time) * 1000, 2),
            "plugin_type": "python"
        }
        print(f"Error processing request: {e}")
        return jsonify(error_response), 500

if __name__ == '__main__':
    port = 80
    print(f"=== Python CMS Plugin starting on port {port} ===")
    print(f"Available actions: product.created, order.created, analytics.calculate")
    print(f"Health endpoint: /health")
    print(f"Actions discovery: /actions")
    
    try:
        # Start Flask server in production mode
        print(f"Starting Flask production server on 0.0.0.0:{port}")
        app.run(
            host='0.0.0.0', 
            port=port, 
            debug=False,
            use_reloader=False,
            threaded=True,
            processes=1
        )
    except Exception as e:
        print(f"ERROR: Failed to start Flask server: {e}")
        import traceback
        traceback.print_exc()
        # Keep the process alive even if Flask fails
        import time
        while True:
            print("Flask server failed, keeping process alive...")
            time.sleep(10) 