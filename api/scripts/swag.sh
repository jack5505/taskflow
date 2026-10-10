#!/bin/sh
# Единственное место, где описано, как генерируется Swagger (Makefile и CI зовут этот скрипт).
# Запускать из каталога api/.
set -eu
exec go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/taskflow/main.go -o internal/docs --parseInternal
