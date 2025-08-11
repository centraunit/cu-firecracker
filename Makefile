# Build and deploy the CMS
.PHONY: deploy dev test clean unit-tests

deploy:
	@echo "Deploying CMS in production mode..."
	@echo "Building CMS image..."
	docker build -t centraunit/cu-firecracker-cms:latest ./cu-cms
	@echo "Building cms-starter..."
	cd ./cms-starter && go build -o bin/cms-starter
	@echo "Restarting CMS..."
	./cms-starter/bin/cms-starter restart

dev:
	@echo "Starting CMS in development mode..."
	@echo "Building CMS dev image..."
	docker build -t centraunit/cu-firecracker-cms:dev ./cu-cms
	@echo "Building cms-starter..."
	cd ./cms-starter && go build -o bin/cms-starter
	@echo "Starting CMS in dev mode..."
	./cms-starter/bin/cms-starter start --dev

test: unit-tests
	@echo "Running comprehensive test suite..."
	@echo "Step 1: Unit tests completed ✓"
	@echo "Step 2: Building cms-starter and running CMS tests..."
	cd ./cms-starter && go build -o bin/cms-starter && \
	./bin/cms-starter start --test

unit-tests:
	@echo "Running unit tests..."
	@echo "Testing cms-starter..."
	cd ./cms-starter && go test -v ./...
	@echo "✓ cms-starter unit tests passed!"

clean:
	@echo "Cleaning up..."
	@echo "Stopping CMS containers..."
	./cms-starter/bin/cms-starter stop || true
	@echo "Removing CMS images..."
	docker rmi centraunit/cu-firecracker-cms:latest centraunit/cu-firecracker-cms:dev centraunit/cu-firecracker-cms:test || true
	@echo "✓ Cleanup complete!"
