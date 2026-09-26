#!/bin/bash
# Run all tests in the container
set -e

echo "=== Running Backend Tests (pytest) ==="
cd /app
python -m pytest app/tests -v --tb=short 2>&1

echo ""
echo "=== Running Frontend Tests (vitest) ==="
cd /app/web
npm test 2>&1

echo ""
echo "=== All Tests Complete ==="
