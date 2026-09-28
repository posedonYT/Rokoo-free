.PHONY: test test-integration test-e2e cover proto up down

test:
	cd backend && GOTOOLCHAIN=local go test -race ./pkg/...
	cd backend && for d in services/*/; do (cd $$d && GOTOOLCHAIN=local go test -race ./...); done

test-integration:
	cd backend && GOTOOLCHAIN=local go test -race -tags integration ./pkg/...
	cd backend && for d in services/*/; do (cd $$d && GOTOOLCHAIN=local go test -race -tags integration ./...); done

test-e2e:
	cd frontend && npm test && npx playwright test

cover:
	cd backend && GOTOOLCHAIN=local go test -race -coverprofile=cover.out ./pkg/redisx/ ./pkg/outbox/ ./services/voice/internal/sfu/ ./services/voice/internal/registry/ ./services/voice/internal/turn/ ./services/realtime/internal/state/ ./services/auth/internal/service/
	cd backend && GOTOOLCHAIN=local go tool cover -func=cover.out | tail -n 1

proto:
	cd backend/proto && buf generate

up:
	docker compose -f backend/deploy/docker-compose.yml up --build

down:
	docker compose -f backend/deploy/docker-compose.yml down
