
.PHONY: start stop run auth token authapi

start:
	docker-compose up -d && echo http://localhost:8080

stop:
	docker-compose down

run:
	go run cmd/main.go

auth:
	curl -X POST http://localhost:8080/realms/myrealm/protocol/openid-connect/token \
		-H 'Content-Type: application/x-www-form-urlencoded' \
		-d 'client_id=my-api' \
		-d 'grant_type=password' \
		-d 'username=antonio' \
		-d 'password=password' | jq .

token:
	curl -s -X POST \
		http://localhost:8080/realms/myrealm/protocol/openid-connect/token \
		-H 'Content-Type: application/x-www-form-urlencoded' \
		-d 'client_id=my-api' \
		-d 'grant_type=password' \
		-d 'username=antonio' \
		-d 'password=password' \
		| jq -r '.access_token'

authapi:
	@echo "Use the following command to call the API with the access token:"
	@echo "curl http://localhost:8081/api/v1/profile -H 'Authorization: Bearer <ACCESS_TOKEN>'"