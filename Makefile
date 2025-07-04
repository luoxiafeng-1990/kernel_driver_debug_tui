# RISC-V BPF调试器构建脚本

# 编译器和标志
GO := go
CC := gcc
CLANG := clang
CFLAGS := -Wall -Wextra -O2
BPF_CFLAGS := -O2 -target bpf -c

# 目标文件
DEBUGGER_BIN := debug-gocui
BPF_EXAMPLE := enhanced_bpf_example.o

.PHONY: all clean help check-deps install-deps debugger-only bpf-only

# 默认只构建调试器（BPF编译可选）
all: check-go-deps $(DEBUGGER_BIN)

# 包含BPF的完整构建
all-with-bpf: check-deps $(DEBUGGER_BIN) $(BPF_EXAMPLE)

# 只构建调试器
debugger-only: check-go-deps $(DEBUGGER_BIN)

# 只构建BPF程序
bpf-only: check-bpf-deps $(BPF_EXAMPLE)

# 构建主调试器（集成BPF数据接收功能）
$(DEBUGGER_BIN): *.go
	@echo "构建TUI调试器（集成录制回放功能）..."
	$(GO) build -o $(DEBUGGER_BIN) .

# 构建增强版BPF示例
$(BPF_EXAMPLE): bpf_templates/enhanced_bpf_template.c
	@echo "构建增强版BPF程序示例..."
	$(CLANG) $(BPF_CFLAGS) bpf_templates/enhanced_bpf_template.c -o $(BPF_EXAMPLE)

# 检查所有依赖
check-deps: check-go-deps check-bpf-deps

# 检查Go依赖
check-go-deps:
	@echo "检查Go依赖..."
	@command -v $(GO) >/dev/null 2>&1 || { echo "错误: 需要安装Go语言环境"; exit 1; }
	@echo "✅ Go环境检查通过"

# 检查BPF依赖
check-bpf-deps:
	@echo "检查BPF依赖..."
	@command -v $(CC) >/dev/null 2>&1 || { echo "错误: 需要安装GCC编译器"; exit 1; }
	@command -v $(CLANG) >/dev/null 2>&1 || { echo "错误: 需要安装Clang编译器"; exit 1; }
	@pkg-config --exists libbpf || { echo "警告: 未找到libbpf开发包，可能需要手动安装"; }
	@echo "✅ BPF环境检查通过"

# 安装依赖 (Ubuntu/Debian)
install-deps:
	@echo "安装依赖包 (需要sudo权限)..."
	sudo apt update
	sudo apt install -y golang-go gcc clang libbpf-dev linux-headers-$(shell uname -r)

# 安装依赖 (CentOS/RHEL)
install-deps-centos:
	@echo "安装依赖包 (需要sudo权限)..."
	sudo yum install -y golang gcc clang libbpf-devel kernel-devel

# 清理构建文件
clean:
	@echo "清理构建文件..."
	rm -f $(DEBUGGER_BIN) $(BPF_EXAMPLE)
	rm -f debug_breakpoints.bpf.c debug_breakpoints.bpf.o
	rm -f load_debug_bpf.sh unload_debug_bpf.sh
	rm -f .debug_breakpoints.json
	rm -f *.frames  # 清理调试会话文件

# 运行调试器
run: $(DEBUGGER_BIN)
	@echo "启动调试器..."
	./$(DEBUGGER_BIN)

# 测试调试器（模拟模式，无需BPF）
test: $(DEBUGGER_BIN)
	@echo "🧪 测试时间旅行调试器（模拟模式）..."
	@echo "启动调试器以测试录制回放功能..."
	@echo "提示：使用模拟数据，每2秒自动生成调试帧"
	./$(DEBUGGER_BIN)

# 测试调试器（完整模式，包含BPF）
test-with-bpf: $(DEBUGGER_BIN) $(BPF_EXAMPLE)
	@echo "🧪 测试时间旅行调试器（完整模式）..."
	@echo "启动调试器以测试录制回放功能..."
	./$(DEBUGGER_BIN)

# 显示帮助
help:
	@echo "🎯 RISC-V BPF调试器构建脚本 - 时间旅行调试版本"
	@echo ""
	@echo "🏗️ 架构说明:"
	@echo "  • 集成式设计：BPF数据接收功能已集成到TUI中"
	@echo "  • 录制回放：支持调试会话录制和帧级回放"
	@echo "  • 时间旅行：可在任意调试帧间跳转"
	@echo ""
	@echo "🎯 构建目标:"
	@echo "  all              - 构建调试器（推荐，无需BPF）"
	@echo "  all-with-bpf     - 构建调试器+BPF程序（需要完整环境）"
	@echo "  debugger-only    - 只构建调试器"
	@echo "  bpf-only         - 只构建BPF程序"
	@echo ""
	@echo "🔧 依赖检查:"
	@echo "  check-deps       - 检查所有依赖"
	@echo "  check-go-deps    - 只检查Go依赖"
	@echo "  check-bpf-deps   - 只检查BPF依赖"
	@echo "  install-deps     - 安装依赖 (Ubuntu/Debian)"
	@echo "  install-deps-centos - 安装依赖 (CentOS/RHEL)"
	@echo ""
	@echo "🏃 运行和测试:"
	@echo "  run              - 运行调试器"
	@echo "  test             - 测试调试器（模拟模式）"
	@echo "  test-with-bpf    - 测试调试器（完整模式）"
	@echo "  clean            - 清理构建文件"
	@echo "  help             - 显示此帮助"
	@echo ""
	@echo "🎬 新功能（录制回放）:"
	@echo "  • start-recording  - 开始录制调试会话"
	@echo "  • stop-recording   - 停止录制并保存.frames文件"
	@echo "  • load-session     - 加载调试会话进行回放"
	@echo "  • F9/F10键         - 在回放模式下导航帧"
	@echo "  • jump-frame <n>   - 跳转到指定帧"
	@echo "  • frame-info       - 查看当前帧信息"
	@echo ""
	@echo "使用示例:"
	@echo "  make install-deps  # 安装依赖"
	@echo "  make all           # 构建所有组件"
	@echo "  make run           # 运行调试器"
	@echo ""
	@echo "🔥 调试器使用流程（录制模式）:"
	@echo "  1. ./$(DEBUGGER_BIN)                    # 启动调试器"
	@echo "  2. 在调试器中: open /path/to/project    # 打开项目"
	@echo "  3. 在调试器中: start-recording          # 开始录制"
	@echo "  4. 双击代码行设置断点"
	@echo "  5. 在调试器中: generate                 # 生成BPF代码"
	@echo "  6. 在调试器中: compile                  # 编译BPF代码"
	@echo "  7. sudo ./load_debug_bpf.sh             # 加载BPF程序"
	@echo "  8. 运行你的内核模块触发断点              # 产生调试帧"
	@echo "  9. 在调试器中: stop-recording           # 停止录制，保存.frames文件"
	@echo " 10. 在调试器中: load-session <file>      # 加载会话进行回放分析"
	@echo " 11. 使用F9/F10键浏览帧                   # 时间旅行调试" 