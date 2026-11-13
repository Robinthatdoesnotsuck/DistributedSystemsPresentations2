change_docker_to_minikube_daemon:
	eval $(minikube docker-env)
build_server:
	minikube image build -t grpc-server:latest -f server/Dockerfile .
build_client:	
	minikube image build -t grpc-client:latest -f client/Dockerfile .
