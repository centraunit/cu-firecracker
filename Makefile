# Build and deploy the CMS
.PHONY: all deploy

all: deploy

deploy:
	cd ./cu-cms && \
	docker build -t issaprodev/cu-cms:latest . && \
	docker push issaprodev/cu-cms:latest && \
	cd ../cms-starter && \
	./bin/cms-starter restart
