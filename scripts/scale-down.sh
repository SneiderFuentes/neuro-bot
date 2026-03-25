#!/bin/bash
# =============================================================================
# SCALE DOWN — Vuelve al perfil normal (default)
# =============================================================================
# Uso: ./scripts/scale-down.sh
#
# Este script reinicia los contenedores con la configuracion base,
# desactivando el perfil high-load.
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${YELLOW}[SCALE-DOWN] Volviendo al perfil normal...${NC}"

if [ ! -f docker-compose.yml ]; then
    echo -e "${RED}Error: docker-compose.yml no encontrado en $PROJECT_DIR${NC}"
    exit 1
fi

echo -e "${YELLOW}[1/3] Reiniciando con configuracion base...${NC}"
docker compose up -d --build

echo -e "${YELLOW}[2/3] Esperando que servicios esten saludables...${NC}"
timeout 60 bash -c 'until docker inspect --format="{{.State.Health.Status}}" neuro_bot_db 2>/dev/null | grep -q healthy; do sleep 2; done' || {
    echo -e "${RED}DB no alcanzo estado healthy en 60s${NC}"
    exit 1
}
timeout 30 bash -c 'until docker inspect --format="{{.State.Health.Status}}" neuro_bot 2>/dev/null | grep -q healthy; do sleep 2; done' || {
    echo -e "${RED}Bot no alcanzo estado healthy en 30s${NC}"
    exit 1
}

echo -e "${YELLOW}[3/3] Verificando servicios...${NC}"
docker compose ps

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  Perfil NORMAL restaurado${NC}"
echo -e "${GREEN}  Workers: 10 | Queue: 100${NC}"
echo -e "${GREEN}  DB conns: 25/10 | MySQL max: 50${NC}"
echo -e "${GREEN}  Bot RAM: 256M | DB RAM: 1024M${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "Para escalar: ${YELLOW}./scripts/scale-up.sh${NC}"
