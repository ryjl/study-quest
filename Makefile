.PHONY: build run test lint docker-build docker-run clean migrate

# 编译 Go 后端
build:
	@echo "==> Building Go Backend..."
	@cd backend && go build -o bin/server cmd/server/main.go

# 运行 Go 后端开发服务器
run:
	@echo "==> Running Go Backend..."
	@cd backend && go run cmd/server/main.go

# 运行 Go 测试
test:
	@echo "==> Testing Go Backend..."
	@cd backend && go test -v ./...

# 构建 Docker 镜像
docker-build:
	@echo "==> Building Docker Image..."
	@cd backend && docker build -t studyquest-backend:latest .

# 运行 Docker 容器
docker-run:
	@echo "==> Running Docker Container..."
	@docker run -p 8080:8080 -e DB_PATH=/data/studyquest.db -v $(PWD)/data:/data studyquest-backend:latest

# 清理构建产物
clean:
	@echo "==> Cleaning build artifacts..."
	@rm -rf backend/bin/
	@rm -rf backend/data/
