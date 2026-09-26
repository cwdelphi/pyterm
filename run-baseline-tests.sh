#!/bin/bash
# ==========================================
#  泡鱼工具 基线测试包 v1.0
#  一键运行全部测试
# ==========================================
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPORT_DIR="$SCRIPT_DIR/reports"
SCREENSHOT_DIR="$SCRIPT_DIR/autotest/screenshots"

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}=========================================="
echo "  泡鱼工具 基线测试包 v1.0"
echo -e "==========================================${NC}"

# 创建报告目录
mkdir -p "$REPORT_DIR" "$SCREENSHOT_DIR"

TOTAL_PASS=0
TOTAL_FAIL=0
START_TIME=$(date +%s)

# ── 1. 后端API测试 ──
echo ""
echo -e "${YELLOW}[1/3] 后端API测试 (pytest)${NC}"
cd "$SCRIPT_DIR"
if python -m pytest app/tests -v --tb=short 2>&1; then
    echo -e "${GREEN}  ✓ 后端测试通过${NC}"
    TOTAL_PASS=$((TOTAL_PASS + 1))
else
    echo -e "${RED}  ✗ 后端测试失败${NC}"
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
fi

# ── 2. 前端单元测试 ──
echo ""
echo -e "${YELLOW}[2/3] 前端单元测试 (vitest)${NC}"
cd "$SCRIPT_DIR/web"
if npm test 2>&1; then
    echo -e "${GREEN}  ✓ 前端测试通过${NC}"
    TOTAL_PASS=$((TOTAL_PASS + 1))
else
    echo -e "${RED}  ✗ 前端测试失败${NC}"
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
fi

# ── 3. E2E浏览器测试 ──
echo ""
echo -e "${YELLOW}[3/3] E2E浏览器测试 (playwright)${NC}"
cd "$SCRIPT_DIR/autotest"
if npx playwright test --reporter=html 2>&1; then
    echo -e "${GREEN}  ✓ E2E测试通过${NC}"
    TOTAL_PASS=$((TOTAL_PASS + 1))
else
    echo -e "${RED}  ✗ E2E测试失败${NC}"
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
fi

END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))

echo ""
echo -e "${BLUE}=========================================="
echo "  测试完成! 耗时: ${ELAPSED}s"
echo "==========================================${NC}"
echo ""
echo "  报告位置:"
echo "  - 后端: $REPORT_DIR/"
echo "  - E2E:  $SCRIPT_DIR/autotest/playwright-report/"
echo "  - 截图: $SCREENSHOT_DIR/"
echo ""

if [ $TOTAL_FAIL -gt 0 ]; then
    echo -e "${RED}  结果: $TOTAL_FAIL 项失败${NC}"
    exit 1
else
    echo -e "${GREEN}  结果: 全部通过!${NC}"
    exit 0
fi
