# Universal Kernel Debugger TUI - Multi-Architecture Makefile
# 支持架构: riscv64, x86_64, arm64
# 目标文件: kernel_debugger_tui.c

# 默认架构 (可通过 make ARCH=xxx 覆盖)
ARCH ?= riscv64

# 程序名称
PROGRAM = kernel_debugger_tui
SOURCE = kernel_debugger_tui.c

# 基础编译选项
CFLAGS = -O2 -g -Wall -std=c99 -D_GNU_SOURCE
LDFLAGS = 
LIBS = -lpthread

# 根据架构设置工具链和编译选项
ifeq ($(ARCH),riscv64)
    # RISC-V 64位配置
    CROSS_COMPILE = riscv64-unknown-linux-gnu-
    SYSROOT = /workshop-debian/output/products/taco_mes20/ea65xx/ubuntu_server/host/riscv64-buildroot-linux-gnu/sysroot
    CC = /workshop-debian/output/products/taco_mes20/ea65xx/ubuntu_server/host/bin/$(CROSS_COMPILE)gcc
    STRIP = /workshop-debian/output/products/taco_mes20/ea65xx/ubuntu_server/host/bin/$(CROSS_COMPILE)strip
    
    # RISC-V特定选项
    CFLAGS += --sysroot=$(SYSROOT)
    LDFLAGS += --sysroot=$(SYSROOT) -L$(SYSROOT)/lib -L$(SYSROOT)/usr/lib
    
    # ncurses库 - 静态链接避免GLIBC版本问题
    LIBS += -Wl,-Bstatic -lpanel -lncurses -ltinfo -Wl,-Bdynamic -ldl -lrt
    
    TARGET_SUFFIX = _riscv64
    ARCH_DESC = RISC-V 64位

else ifeq ($(ARCH),x86_64)
    # x86_64配置
    CC = gcc
    STRIP = strip
    
    # x86_64特定选项
    CFLAGS += -m64
    
    # ncurses库 - 动态链接 (使用宽字符支持)
    LIBS += -lncursesw -lpanel
    
    TARGET_SUFFIX = _x86_64
    ARCH_DESC = x86_64

else ifeq ($(ARCH),arm64)
    # ARM64配置
    CROSS_COMPILE = aarch64-linux-gnu-
    CC = $(CROSS_COMPILE)gcc
    STRIP = $(CROSS_COMPILE)strip
    
    # ARM64特定选项 (如果有特定的sysroot可以在这里配置)
    # SYSROOT = /path/to/arm64/sysroot
    # CFLAGS += --sysroot=$(SYSROOT)
    # LDFLAGS += --sysroot=$(SYSROOT)
    
    # ncurses库 (使用宽字符支持)
    LIBS += -lncursesw -lpanel
    
    TARGET_SUFFIX = _arm64
    ARCH_DESC = ARM64

else
    $(error 不支持的架构: $(ARCH). 支持的架构: riscv64, x86_64, arm64)
endif

# 最终目标文件名
TARGET = $(PROGRAM)$(TARGET_SUFFIX)

# 默认目标
all: $(TARGET)

# 编译目标
$(TARGET): $(SOURCE)
	@echo "🔨 编译 $(PROGRAM) ($(ARCH_DESC))..."
	@echo "   源文件: $(SOURCE)"
	@echo "   编译器: $(CC)"
	@echo "   目标文件: $(TARGET)"
	@if [ "$(ARCH)" = "riscv64" ]; then \
		echo "   SYSROOT: $(SYSROOT)"; \
		if [ ! -f "$(CC)" ]; then \
			echo "❌ 错误: RISC-V工具链不存在: $(CC)"; \
			echo "   请确保Docker环境中有正确的工具链"; \
			exit 1; \
		fi; \
		if [ ! -d "$(SYSROOT)" ]; then \
			echo "❌ 错误: SYSROOT不存在: $(SYSROOT)"; \
			exit 1; \
		fi; \
	elif [ "$(ARCH)" = "arm64" ]; then \
		if ! command -v $(CC) >/dev/null 2>&1; then \
			echo "❌ 错误: ARM64工具链不存在: $(CC)"; \
			echo "   请安装: sudo apt-get install gcc-aarch64-linux-gnu"; \
			exit 1; \
		fi; \
	elif [ "$(ARCH)" = "x86_64" ]; then \
		if ! command -v $(CC) >/dev/null 2>&1; then \
			echo "❌ 错误: 编译器不存在: $(CC)"; \
			exit 1; \
		fi; \
	fi
	$(CC) $(CFLAGS) $(LDFLAGS) -o $(TARGET) $(SOURCE) $(LIBS)
	@echo "✅ 编译完成"
	@echo "   文件大小: $$(ls -lh $(TARGET) | awk '{print $$5}')"
	@echo "   架构信息: $$(file $(TARGET) | cut -d: -f2-)"

# 架构特定目标
riscv64:
	@$(MAKE) ARCH=riscv64

x86_64:
	@$(MAKE) ARCH=x86_64

arm64:
	@$(MAKE) ARCH=arm64

# 编译所有架构
all-arch: clean
	@echo "🔨 编译所有支持的架构..."
	@$(MAKE) ARCH=riscv64
	@$(MAKE) ARCH=x86_64
	@$(MAKE) ARCH=arm64
	@echo "✅ 所有架构编译完成"
	@ls -la $(PROGRAM)_*

# 测试编译结果
test: $(TARGET)
	@echo "🧪 测试编译结果..."
	@if [ -f $(TARGET) ] && [ -x $(TARGET) ]; then \
		echo "✅ $(TARGET): 可执行文件存在"; \
		echo "   大小: $$(ls -lh $(TARGET) | awk '{print $$5}')"; \
		echo "   类型: $$(file $(TARGET))"; \
	else \
		echo "❌ $(TARGET): 编译失败或文件不可执行"; \
		exit 1; \
	fi

# 检查工具链
check-toolchain:
	@echo "🔍 检查 $(ARCH_DESC) 工具链..."
	@echo "目标架构: $(ARCH)"
	@echo "编译器: $(CC)"
	@if [ "$(ARCH)" = "riscv64" ]; then \
		if [ -f "$(CC)" ]; then \
			echo "✅ RISC-V工具链: $$($(CC) --version | head -1)"; \
		else \
			echo "❌ RISC-V工具链不存在: $(CC)"; \
		fi; \
		if [ -d "$(SYSROOT)" ]; then \
			echo "✅ SYSROOT: $(SYSROOT)"; \
		else \
			echo "❌ SYSROOT不存在: $(SYSROOT)"; \
		fi; \
	elif [ "$(ARCH)" = "arm64" ]; then \
		if command -v $(CC) >/dev/null 2>&1; then \
			echo "✅ ARM64工具链: $$($(CC) --version | head -1)"; \
		else \
			echo "❌ ARM64工具链不存在，请安装: sudo apt-get install gcc-aarch64-linux-gnu"; \
		fi; \
	elif [ "$(ARCH)" = "x86_64" ]; then \
		if command -v $(CC) >/dev/null 2>&1; then \
			echo "✅ x86_64编译器: $$($(CC) --version | head -1)"; \
		else \
			echo "❌ x86_64编译器不存在"; \
		fi; \
	fi

# 安装依赖 (针对不同架构)
deps:
	@echo "📦 安装编译依赖..."
	@if [ "$(ARCH)" = "x86_64" ]; then \
		echo "安装x86_64依赖..."; \
		sudo apt-get update; \
		sudo apt-get install -y build-essential libncurses5-dev libncursesw5-dev; \
	elif [ "$(ARCH)" = "arm64" ]; then \
		echo "安装ARM64交叉编译依赖..."; \
		sudo apt-get update; \
		sudo apt-get install -y gcc-aarch64-linux-gnu; \
		sudo apt-get install -y libncurses5-dev:arm64 || echo "⚠️  无法安装ARM64 ncurses库，可能需要手动配置"; \
	elif [ "$(ARCH)" = "riscv64" ]; then \
		echo "RISC-V工具链应该已在Docker环境中配置"; \
		echo "检查工具链状态..."; \
		$(MAKE) check-toolchain; \
	fi
	@echo "✅ 依赖安装完成"

# 清理
clean:
	@echo "🧹 清理编译文件..."
	rm -f $(PROGRAM)_riscv64 $(PROGRAM)_x86_64 $(PROGRAM)_arm64
	@echo "✅ 清理完成"

# 显示帮助
help:
	@echo "Universal Kernel Debugger TUI - 多架构编译"
	@echo "=========================================="
	@echo ""
	@echo "支持的架构:"
	@echo "  riscv64  - RISC-V 64位 (默认)"
	@echo "  x86_64   - Intel/AMD 64位"
	@echo "  arm64    - ARM 64位"
	@echo ""
	@echo "使用方法:"
	@echo "  make                    # 编译默认架构 (riscv64)"
	@echo "  make ARCH=riscv64       # 编译RISC-V版本"
	@echo "  make ARCH=x86_64        # 编译x86_64版本"
	@echo "  make ARCH=arm64         # 编译ARM64版本"
	@echo "  make riscv64            # 编译RISC-V版本"
	@echo "  make x86_64             # 编译x86_64版本"
	@echo "  make arm64              # 编译ARM64版本"
	@echo "  make all-arch           # 编译所有架构"
	@echo ""
	@echo "其他目标:"
	@echo "  make test               # 测试编译结果"
	@echo "  make check-toolchain    # 检查工具链"
	@echo "  make deps               # 安装依赖"
	@echo "  make clean              # 清理文件"
	@echo "  make help               # 显示帮助"
	@echo ""
	@echo "示例:"
	@echo "  make ARCH=x86_64        # 在本机编译x86_64版本"
	@echo "  make ARCH=riscv64       # 在Docker中交叉编译RISC-V版本"
	@echo "  make all-arch           # 编译所有支持的架构"

.PHONY: all riscv64 x86_64 arm64 all-arch test check-toolchain deps clean help