#!/bin/bash
set -e

echo "====================================="
echo "E2B Infrastructure - Quick Start"
echo "====================================="

# Check Docker is installed
if ! command -v docker &> /dev/null; then
    echo "Error: Docker is not installed. Please install Docker first."
    exit 1
fi

# Check Docker Compose is installed
if ! docker compose version &> /dev/null && ! docker-compose version &> /dev/null; then
    echo "Error: Docker Compose is not installed. Please install Docker Compose first."
    exit 1
fi

# Determine docker-compose command
if docker compose version &> /dev/null; then
    COMPOSE_CMD="docker compose"
else
    COMPOSE_CMD="docker-compose"
fi

echo ""
echo "Step 1: Preparing build contexts..."
./docker-compose-build.sh minimal

echo ""
echo "Step 2: Starting minimal services..."
echo "This includes: PostgreSQL, Redis, MinIO (S3 replacement), and API"
echo ""

$COMPOSE_CMD -f docker-compose.minimal.yml up -d

echo ""
echo "Step 3: Waiting for services to be ready..."
sleep 10

echo ""
echo "Step 4: Checking service health..."
$COMPOSE_CMD -f docker-compose.minimal.yml ps

echo ""
echo "====================================="
echo "Services are starting up!"
echo "====================================="
echo ""
echo "Available endpoints:"
echo "  - API: http://localhost:3000"
echo "  - PostgreSQL: localhost:5432 (user: e2b, password: e2b_password)"
echo "  - Redis: localhost:6379"
echo "  - MinIO Console: http://localhost:9001 (user: minioadmin, password: minioadmin)"
echo ""
echo "To view logs:"
echo "  $COMPOSE_CMD -f docker-compose.minimal.yml logs -f"
echo ""
echo "To stop services:"
echo "  $COMPOSE_CMD -f docker-compose.minimal.yml down"
echo ""
echo "To run the full stack instead:"
echo "  ./docker-compose-build.sh"
echo "  $COMPOSE_CMD up -d"
echo "" 