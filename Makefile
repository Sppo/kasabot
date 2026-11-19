.PHONY: check test build dev tunnel

PORT ?= 8080

# Перевірка форматування, архітектури, статичний аналіз та всі автотести (Go + Frontend якщо папка web вже існує)
check:
	@test -z "$$(gofmt -l .)" || (echo "Невідформатовані Go-файли (запустіть gofmt -w .):" && gofmt -l . && exit 1)
	go vet ./...
	go test -v ./...
	go build ./...
	@if [ -f web/package.json ]; then cd web && npm run build; fi

# Швидкий запуск автотестів (включно з arch_test.go)
test:
	go test -v ./...

# Збірка продакшн-бінарника
build:
	@if [ -f web/package.json ]; then cd web && npm run build; fi
	@if [ -d cmd/app ]; then go build -o bin/kasabot ./cmd/app; else echo "cmd/app ще не створено (див. План #006)"; fi

# Локальний запуск бекенду
dev:
	@if [ -d cmd/app ]; then go run ./cmd/app; else echo "cmd/app ще не створено (див. План #006)"; fi

# Запуск HTTPS-тунелю для тестування мікрофона на iPhone (потребує встановленого cloudflared)
tunnel:
	@PORT_VAL="$(PORT)"; \
	if [ -f .env ] && [ -z "$${PORT_OVERRIDE:-}" ]; then \
		ENV_P=$$(grep -E '^(export[[:space:]]+)?PORT=' .env | tail -n 1 | cut -d= -f2- | cut -d'#' -f1 | tr -d ' "'"'"''); \
		if [ -n "$$ENV_P" ]; then PORT_VAL="$$ENV_P"; fi; \
	fi; \
	cloudflared tunnel --url "http://localhost:$$PORT_VAL"
