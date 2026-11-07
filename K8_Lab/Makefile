change_docker_to_minikube_daemon:
	eval $(minikube docker-env)
build_server:
	docker build -t grpc-server:latest -f server/Dockerfile .
build_client:	
	docker build -t grpc-client:latest -f client/Dockerfile .
