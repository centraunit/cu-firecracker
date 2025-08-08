# Build and deploy the CMS
.PHONY: all deploy

all: deploy

deploy:
	cd ./cu-cms && \
	docker build -t centraunit/cu-firecracker-cms:local . && \
	cd ../cms-starter && go build -o bin/cms-starter && \
	./bin/cms-starter restart
