#!/bin/bash
# =============================================================================
# SCALE UP — Activa perfil high-load (~1000 chats/hora)
# =============================================================================
# Uso: ./scripts/scale-up.sh
#
# Este script reconstruye los contenedores con el perfil high-load
# usando rolling restart para minimizar downtime.
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${YELLOW}[SCALE-UP] Activando perfil high-load...${NC}"

# Verificar que los archivos existen
if [ ! -f docker-compose.yml ]; then
    echo -e "${RED}Error: docker-compose.yml no encontrado en $PROJECT_DIR${NC}"
    exit 1
fi
if [ ! -f docker-compose.high-load.yml ]; then
    echo -e "${RED}Error: docker-compose.high-load.yml no encontrado${NC}"
    exit 1
fi
if [ ! -f .env.high-load ]; then
    echo -e "${RED}Error: .env.high-load no encontrado${NC}"
    exit 1
fi

echo -e "${YELLOW}[1/4] Reconstruyendo imagen del bot...${NC}"
docker compose -f docker-compose.yml -f docker-compose.high-load.yml build bot

echo -e "${YELLOW}[2/4] Escalando base de datos (sin downtime)...${NC}"
docker compose -f docker-compose.yml -f docker-compose.high-load.yml up -d db
echo "Esperando que DB este saludable..."
timeout 60 bash -c 'until docker inspect --format="{{.State.Health.Status}}" neuro_bot_db 2>/dev/null | grep -q healthy; do sleep 2; done' || {
    echo -e "${RED}DB no alcanzo estado healthy en 60s${NC}"
    exit 1
}

echo -e "${YELLOW}[3/4] Reiniciando bot con perfil high-load...${NC}"
docker compose -f docker-compose.yml -f docker-compose.high-load.yml up -d bot
echo "Esperando que bot este saludable..."
timeout 30 bash -c 'until docker inspect --format="{{.State.Health.Status}}" neuro_bot 2>/dev/null | grep -q healthy; do sleep 2; done' || {
    echo -e "${RED}Bot no alcanzo estado healthy en 30s${NC}"
    exit 1
}

echo -e "${YELLOW}[4/4] Verificando servicios...${NC}"
docker compose -f docker-compose.yml -f docker-compose.high-load.yml ps

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  HIGH-LOAD activado exitosamente${NC}"
echo -e "${GREEN}  Workers: 50 | Queue: 500${NC}"
echo -e "${GREEN}  DB conns: 50/50 | MySQL max: 200${NC}"
echo -e "${GREEN}  Bot RAM: 512M | DB RAM: 1024M${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "Para volver a normal: ${YELLOW}./scripts/scale-down.sh${NC}"
