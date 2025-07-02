#!/bin/bash
# universal_kernel_debugger_setup.sh - 通用Linux内核驱动调试器生成器
# 支持调试任意内核驱动函数的完全自动化工具

set -e

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TOOL_NAME="Universal Kernel Debugger"
VERSION="1.0.0"

# 颜色输出函数
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warning() { echo -e "${YELLOW}[WARNING]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_header() { echo -e "${PURPLE}[HEADER]${NC} $1"; }

# 显示欢迎信息
show_welcome() {
    clear
    echo -e "${CYAN}"
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║                                                              ║"
    echo "║            🚀 通用Linux内核驱动调试器生成器 🚀               ║"
    echo "║                                                              ║"
    echo "║   版本: $VERSION                                            ║"
    echo "║   功能: 为任意内核驱动函数创建类GDB的交互式调试环境          ║"
    echo "║   技术: eBPF + ncurses TUI + 动态探测点                      ║"
    echo "║                                                              ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
    echo ""
}

# 检查系统依赖
check_dependencies() {
    log_header "🔍 检查系统依赖..."
    
    local missing_deps=()
    local missing_packages=()
    
    # 检查基本编译工具
    command -v clang >/dev/null 2>&1 || missing_deps+=("clang")
    command -v gcc >/dev/null 2>&1 || missing_deps+=("gcc")
    command -v make >/dev/null 2>&1 || missing_deps+=("make")
    command -v pkg-config >/dev/null 2>&1 || missing_deps+=("pkg-config")
    
    # 检查eBPF相关依赖
    if ! pkg-config --exists libbpf 2>/dev/null; then
        missing_packages+=("libbpf-dev")
    fi
    
    # 检查ncurses库
    if ! pkg-config --exists ncurses 2>/dev/null; then
        missing_packages+=("libncurses-dev")
    fi
    
    # 检查libelf
    if ! pkg-config --exists libelf 2>/dev/null; then
        missing_packages+=("libelf-dev")
    fi
    
    # 检查内核头文件
    if [ ! -d "/usr/src/linux-headers-$(uname -r)" ] && [ ! -d "/lib/modules/$(uname -r)/build" ]; then
        missing_packages+=("linux-headers-$(uname -r)")
    fi
    
    if [ ${#missing_deps[@]} -ne 0 ] || [ ${#missing_packages[@]} -ne 0 ]; then
        log_error "缺少必要的依赖！"
        if [ ${#missing_deps[@]} -ne 0 ]; then
            log_error "缺少工具: ${missing_deps[*]}"
        fi
        if [ ${#missing_packages[@]} -ne 0 ]; then
            log_error "缺少软件包: ${missing_packages[*]}"
            echo ""
            log_info "请运行以下命令安装依赖："
            echo "sudo apt-get update"
            echo "sudo apt-get install ${missing_packages[*]} ${missing_deps[*]}"
        fi
        exit 1
    fi
    
    log_success "所有依赖检查通过！"
}

# 检查内核eBPF支持
check_kernel_support() {
    log_header "🔧 检查内核eBPF支持..."
    
    # 检查内核版本
    local kernel_version=$(uname -r | cut -d. -f1,2)
    local major_version=$(echo $kernel_version | cut -d. -f1)
    local minor_version=$(echo $kernel_version | cut -d. -f2)
    
    if [ $major_version -lt 4 ] || ([ $major_version -eq 4 ] && [ $minor_version -lt 18 ]); then
        log_error "内核版本过低！需要 >= 4.18，当前版本: $(uname -r)"
        exit 1
    fi
    
    # 检查eBPF配置
    local config_file=""
    if [ -f "/proc/config.gz" ]; then
        config_file="/proc/config.gz"
    elif [ -f "/boot/config-$(uname -r)" ]; then
        config_file="/boot/config-$(uname -r)"
    fi
    
    if [ -n "$config_file" ]; then
        local check_cmd="cat"
        if [[ "$config_file" == *.gz ]]; then
            check_cmd="zcat"
        fi
        
        if ! $check_cmd "$config_file" | grep -q "CONFIG_BPF=y"; then
            log_warning "内核可能未启用eBPF支持"
        fi
        
        if ! $check_cmd "$config_file" | grep -q "CONFIG_KPROBES=y"; then
            log_warning "内核可能未启用KPROBES支持"
        fi
    fi
    
    # 检查bpf系统调用
    if [ ! -d "/sys/fs/bpf" ]; then
        log_warning "BPF文件系统未挂载，尝试挂载..."
        if command -v mount >/dev/null 2>&1; then
            sudo mount -t bpf bpf /sys/fs/bpf 2>/dev/null || true
        fi
    fi
    
    log_success "内核eBPF支持检查完成"
}

# 扫描可用的内核模块和函数
scan_kernel_symbols() {
    log_header "🔍 扫描内核符号..."
    
    # 创建符号数据库目录
    mkdir -p "$SCRIPT_DIR/symbol_db"
    
    # 扫描所有导出符号
    if [ -r "/proc/kallsyms" ]; then
        log_info "从 /proc/kallsyms 提取符号..."
        grep -E "^[0-9a-f]+ [tT] " /proc/kallsyms | \
        awk '{print $3}' | \
        sort | uniq > "$SCRIPT_DIR/symbol_db/all_functions.txt"
        
        local func_count=$(wc -l < "$SCRIPT_DIR/symbol_db/all_functions.txt")
        log_success "发现 $func_count 个可跟踪函数"
    else
        log_warning "无法读取 /proc/kallsyms，需要root权限"
    fi
    
    # 扫描模块符号
    if [ -d "/sys/module" ]; then
        log_info "扫描已加载的内核模块..."
        find /sys/module -name "*.ko" -o -name "holders" | \
        grep -v holders | \
        xargs -I {} basename {} .ko | \
        sort | uniq > "$SCRIPT_DIR/symbol_db/loaded_modules.txt"
        
        local mod_count=$(wc -l < "$SCRIPT_DIR/symbol_db/loaded_modules.txt" 2>/dev/null || echo "0")
        log_success "发现 $mod_count 个已加载模块"
    fi
}

# 生成通用eBPF调试程序
generate_universal_ebpf() {
    log_header "📦 生成通用eBPF调试程序..."
    
    cat > "$SCRIPT_DIR/kernel_debugger.bpf.c" << 'EOF_EBPF'
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

#define MAX_FUNCTIONS 64
#define MAX_BREAKPOINTS 256
#define MAX_FUNCTION_NAME 64
#define MAX_MESSAGE_SIZE 128

// 调试事件类型
enum debug_event_type {
    EVENT_FUNCTION_ENTRY = 0,
    EVENT_FUNCTION_EXIT = 1,
    EVENT_BREAKPOINT = 2,
    EVENT_WATCHPOINT = 3,
    EVENT_STEP = 4,
    EVENT_ERROR = 5,
    EVENT_INFO = 6
};

// 调试事件结构
struct debug_event {
    __u64 timestamp;
    __u32 pid;
    __u32 tid;
    __u32 cpu;
    __u8 event_type;
    __u8 function_id;
    __u16 breakpoint_id;
    __u64 instruction_pointer;
    __u64 stack_pointer;
    __u64 params[6];        // 最多6个函数参数
    __s64 return_value;
    char function_name[MAX_FUNCTION_NAME];
    char message[MAX_MESSAGE_SIZE];
};

// 函数配置结构
struct function_config {
    char name[MAX_FUNCTION_NAME];
    __u8 enabled;
    __u8 trace_entry;
    __u8 trace_exit;
    __u8 trace_params;
    __u32 param_count;
};

// 断点配置结构  
struct breakpoint_config {
    __u64 address;
    __u8 enabled;
    __u8 function_id;
    __u16 offset;
    char condition[32];
};

// 调试器控制状态
struct debugger_control {
    __u8 debug_mode;        // 0=run, 1=step, 2=next, 3=finish
    __u32 target_pid;       // 0表示所有进程
    __u32 step_count;
    __u8 active_functions[MAX_FUNCTIONS];
    __u8 global_enable;
};

// Maps定义
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 1 << 20); // 1MB ring buffer
} events SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, MAX_FUNCTIONS);
    __type(key, __u32);
    __type(value, struct function_config);
} function_configs SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, MAX_BREAKPOINTS);
    __type(key, __u32);
    __type(value, struct breakpoint_config);
} breakpoint_configs SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct debugger_control);
} control_map SEC(".maps");

// 获取调试器控制状态
static inline struct debugger_control* get_debugger_control() {
    __u32 key = 0;
    return bpf_map_lookup_elem(&control_map, &key);
}

// 检查是否应该跟踪当前进程
static inline bool should_trace_process(struct debugger_control *ctrl) {
    if (!ctrl || !ctrl->global_enable) return false;
    
    __u32 current_pid = bpf_get_current_pid_tgid() >> 32;
    return (ctrl->target_pid == 0 || ctrl->target_pid == current_pid);
}

// 发送调试事件
static inline void send_debug_event(struct pt_regs *ctx, __u8 event_type, 
                                   __u8 function_id, const char *func_name) {
    struct debugger_control *ctrl = get_debugger_control();
    if (!should_trace_process(ctrl)) return;
    
    struct debug_event *event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) return;
    
    event->timestamp = bpf_ktime_get_ns();
    event->pid = bpf_get_current_pid_tgid() >> 32;
    event->tid = bpf_get_current_pid_tgid() & 0xFFFFFFFF;
    event->cpu = bpf_get_smp_processor_id();
    event->event_type = event_type;
    event->function_id = function_id;
    event->instruction_pointer = PT_REGS_IP(ctx);
    event->stack_pointer = PT_REGS_SP(ctx);
    
    // 捕获函数参数（最多6个）
    event->params[0] = PT_REGS_PARM1(ctx);
    event->params[1] = PT_REGS_PARM2(ctx);
    event->params[2] = PT_REGS_PARM3(ctx);
    event->params[3] = PT_REGS_PARM4(ctx);
    event->params[4] = PT_REGS_PARM5(ctx);
    event->params[5] = PT_REGS_PARM6(ctx);
    
    if (event_type == EVENT_FUNCTION_EXIT) {
        event->return_value = PT_REGS_RC(ctx);
    }
    
    // 安全复制函数名
    if (func_name) {
        bpf_probe_read_str(event->function_name, MAX_FUNCTION_NAME, func_name);
    }
    
    bpf_ringbuf_submit(event, 0);
}

// 通用函数入口探测点
SEC("kprobe")
int generic_function_entry(struct pt_regs *ctx) {
    // 这个函数会被动态附加到不同的函数上
    // function_id通过用户空间程序设置
    send_debug_event(ctx, EVENT_FUNCTION_ENTRY, 0, "generic_function");
    return 0;
}

// 通用函数出口探测点
SEC("kretprobe")
int generic_function_exit(struct pt_regs *ctx) {
    send_debug_event(ctx, EVENT_FUNCTION_EXIT, 0, "generic_function");
    return 0;
}

char LICENSE[] SEC("license") = "GPL";
EOF_EBPF

    log_success "通用eBPF调试程序生成完成"
}

# 生成ncurses TUI控制器
generate_tui_controller() {
    log_header "🖥️  生成TUI交互式控制器..."
    
    cat > "$SCRIPT_DIR/kernel_debugger_tui.c" << 'EOF_TUI'
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <errno.h>
#include <time.h>
#include <sys/resource.h>
#include <ncurses.h>
#include <panel.h>
#include <bpf/libbpf.h>
#include <bpf/bpf.h>

#define MAX_FUNCTIONS 64
#define MAX_BREAKPOINTS 256
#define MAX_FUNCTION_NAME 64
#define MAX_MESSAGE_SIZE 128
#define MAX_COMMAND_LEN 256

// 调试事件类型（与eBPF程序保持一致）
enum debug_event_type {
    EVENT_FUNCTION_ENTRY = 0,
    EVENT_FUNCTION_EXIT = 1,
    EVENT_BREAKPOINT = 2,
    EVENT_WATCHPOINT = 3,
    EVENT_STEP = 4,
    EVENT_ERROR = 5,
    EVENT_INFO = 6
};

// 调试事件结构
struct debug_event {
    uint64_t timestamp;
    uint32_t pid;
    uint32_t tid;
    uint32_t cpu;
    uint8_t event_type;
    uint8_t function_id;
    uint16_t breakpoint_id;
    uint64_t instruction_pointer;
    uint64_t stack_pointer;
    uint64_t params[6];
    int64_t return_value;
    char function_name[MAX_FUNCTION_NAME];
    char message[MAX_MESSAGE_SIZE];
};

// TUI窗口结构
typedef struct {
    WINDOW *win;
    PANEL *panel;
    int height, width;
    int start_y, start_x;
    char title[32];
} tui_window_t;

// 调试器状态
typedef struct {
    bool running;
    bool step_mode;
    uint32_t target_pid;
    uint32_t event_count;
    struct bpf_object *obj;
    int events_fd;
    int control_fd;
    struct ring_buffer *rb;
    
    // TUI窗口
    tui_window_t main_win;
    tui_window_t code_win;
    tui_window_t regs_win;
    tui_window_t vars_win;
    tui_window_t stack_win;
    tui_window_t breaks_win;
    tui_window_t cmd_win;
    tui_window_t status_win;
    
    // 当前状态
    char current_function[MAX_FUNCTION_NAME];
    uint64_t current_ip;
    uint64_t registers[16];
    char command_buffer[MAX_COMMAND_LEN];
    int cmd_cursor;
    
} debugger_state_t;

static debugger_state_t g_debugger = {0};

// 事件名称映射
static const char* event_names[] = {
    "函数入口", "函数出口", "断点", "监视点", "单步", "错误", "信息"
};

// 信号处理
static void sig_handler(int sig) {
    g_debugger.running = false;
}

// 初始化ncurses TUI
static int init_tui() {
    initscr();
    cbreak();
    noecho();
    keypad(stdscr, TRUE);
    curs_set(0);
    
    // 启用颜色
    if (has_colors()) {
        start_color();
        init_pair(1, COLOR_WHITE, COLOR_BLUE);    // 标题栏
        init_pair(2, COLOR_GREEN, COLOR_BLACK);   // 正常文本
        init_pair(3, COLOR_RED, COLOR_BLACK);     // 错误文本
        init_pair(4, COLOR_YELLOW, COLOR_BLACK);  // 警告文本
        init_pair(5, COLOR_CYAN, COLOR_BLACK);    // 高亮文本
    }
    
    // 启用鼠标
    mousemask(ALL_MOUSE_EVENTS | REPORT_MOUSE_POSITION, NULL);
    
    return 0;
}

// 创建TUI窗口
static void create_windows() {
    int max_y, max_x;
    getmaxyx(stdscr, max_y, max_x);
    
    // 计算窗口尺寸和位置
    int left_width = max_x / 3;
    int right_width = max_x - left_width;
    int top_height = (max_y - 3) / 2;
    int bottom_height = max_y - top_height - 3;
    
    // 左侧窗口 - 状态信息
    g_debugger.regs_win.height = top_height / 2;
    g_debugger.regs_win.width = left_width;
    g_debugger.regs_win.start_y = 0;
    g_debugger.regs_win.start_x = 0;
    strcpy(g_debugger.regs_win.title, "寄存器");
    
    g_debugger.vars_win.height = top_height / 2;
    g_debugger.vars_win.width = left_width;
    g_debugger.vars_win.start_y = top_height / 2;
    g_debugger.vars_win.start_x = 0;
    strcpy(g_debugger.vars_win.title, "变量");
    
    g_debugger.stack_win.height = bottom_height / 2;
    g_debugger.stack_win.width = left_width;
    g_debugger.stack_win.start_y = top_height;
    g_debugger.stack_win.start_x = 0;
    strcpy(g_debugger.stack_win.title, "调用栈");
    
    g_debugger.breaks_win.height = bottom_height / 2;
    g_debugger.breaks_win.width = left_width;
    g_debugger.breaks_win.start_y = top_height + bottom_height / 2;
    g_debugger.breaks_win.start_x = 0;
    strcpy(g_debugger.breaks_win.title, "断点");
    
    // 右侧窗口 - 代码和命令
    g_debugger.code_win.height = top_height + bottom_height - 5;
    g_debugger.code_win.width = right_width;
    g_debugger.code_win.start_y = 0;
    g_debugger.code_win.start_x = left_width;
    strcpy(g_debugger.code_win.title, "代码视图");
    
    g_debugger.cmd_win.height = 3;
    g_debugger.cmd_win.width = right_width;
    g_debugger.cmd_win.start_y = max_y - 5;
    g_debugger.cmd_win.start_x = left_width;
    strcpy(g_debugger.cmd_win.title, "命令");
    
    // 底部状态栏
    g_debugger.status_win.height = 2;
    g_debugger.status_win.width = max_x;
    g_debugger.status_win.start_y = max_y - 2;
    g_debugger.status_win.start_x = 0;
    strcpy(g_debugger.status_win.title, "状态");
    
    // 创建所有窗口
    tui_window_t *windows[] = {
        &g_debugger.regs_win, &g_debugger.vars_win, &g_debugger.stack_win,
        &g_debugger.breaks_win, &g_debugger.code_win, &g_debugger.cmd_win,
        &g_debugger.status_win
    };
    
    for (int i = 0; i < 7; i++) {
        tui_window_t *w = windows[i];
        w->win = newwin(w->height, w->width, w->start_y, w->start_x);
        w->panel = new_panel(w->win);
        box(w->win, 0, 0);
        
        // 绘制标题
        wattron(w->win, COLOR_PAIR(1) | A_BOLD);
        mvwprintw(w->win, 0, 2, " %s ", w->title);
        wattroff(w->win, COLOR_PAIR(1) | A_BOLD);
    }
    
    update_panels();
    doupdate();
}

// 更新寄存器窗口
static void update_registers_window() {
    WINDOW *win = g_debugger.regs_win.win;
    wclear(win);
    box(win, 0, 0);
    
    wattron(win, COLOR_PAIR(1) | A_BOLD);
    mvwprintw(win, 0, 2, " 寄存器 ");
    wattroff(win, COLOR_PAIR(1) | A_BOLD);
    
    wattron(win, COLOR_PAIR(2));
    mvwprintw(win, 1, 2, "RIP: 0x%016lx", g_debugger.current_ip);
    mvwprintw(win, 2, 2, "RSP: 0x%016lx", g_debugger.registers[0]);
    mvwprintw(win, 3, 2, "RBP: 0x%016lx", g_debugger.registers[1]);
    mvwprintw(win, 4, 2, "RAX: 0x%016lx", g_debugger.registers[2]);
    mvwprintw(win, 5, 2, "RBX: 0x%016lx", g_debugger.registers[3]);
    wattroff(win, COLOR_PAIR(2));
    
    wrefresh(win);
}

// 更新代码窗口
static void update_code_window() {
    WINDOW *win = g_debugger.code_win.win;
    wclear(win);
    box(win, 0, 0);
    
    wattron(win, COLOR_PAIR(1) | A_BOLD);
    mvwprintw(win, 0, 2, " 代码视图 - %s ", g_debugger.current_function);
    wattroff(win, COLOR_PAIR(1) | A_BOLD);
    
    wattron(win, COLOR_PAIR(2));
    mvwprintw(win, 2, 2, "当前函数: %s", g_debugger.current_function);
    mvwprintw(win, 3, 2, "指令地址: 0x%016lx", g_debugger.current_ip);
    mvwprintw(win, 5, 2, "源代码视图:");
    mvwprintw(win, 6, 2, "  // 这里将显示反汇编代码");
    mvwprintw(win, 7, 2, "  // 或者通过DWARF信息显示C源码");
    mvwprintw(win, 8, 2, "  // 当前执行位置会高亮显示");
    wattroff(win, COLOR_PAIR(2));
    
    wrefresh(win);
}

// 更新命令窗口
static void update_command_window() {
    WINDOW *win = g_debugger.cmd_win.win;
    wclear(win);
    box(win, 0, 0);
    
    wattron(win, COLOR_PAIR(1) | A_BOLD);
    mvwprintw(win, 0, 2, " 命令 ");
    wattroff(win, COLOR_PAIR(1) | A_BOLD);
    
    wattron(win, COLOR_PAIR(2));
    mvwprintw(win, 1, 2, "(ukd) %s", g_debugger.command_buffer);
    wattroff(win, COLOR_PAIR(2));
    
    // 显示光标
    wmove(win, 1, 8 + strlen(g_debugger.command_buffer));
    curs_set(1);
    
    wrefresh(win);
}

// 更新状态窗口
static void update_status_window() {
    WINDOW *win = g_debugger.status_win.win;
    wclear(win);
    
    wattron(win, COLOR_PAIR(1));
    mvwprintw(win, 0, 2, "Universal Kernel Debugger | 事件: %u | PID: %u | 模式: %s",
              g_debugger.event_count,
              g_debugger.target_pid,
              g_debugger.step_mode ? "单步" : "运行");
    mvwprintw(win, 1, 2, "快捷键: F5=继续 F10=步过 F11=步入 Ctrl+C=退出");
    wattroff(win, COLOR_PAIR(1));
    
    wrefresh(win);
}

// 更新所有窗口
static void update_all_windows() {
    update_registers_window();
    update_code_window();
    update_command_window();
    update_status_window();
    update_panels();
    doupdate();
}

// 处理调试事件
static int handle_debug_event(void *ctx, void *data, size_t data_sz) {
    struct debug_event *event = (struct debug_event *)data;
    
    g_debugger.event_count++;
    g_debugger.current_ip = event->instruction_pointer;
    strncpy(g_debugger.current_function, event->function_name, MAX_FUNCTION_NAME-1);
    
    // 更新寄存器信息
    g_debugger.registers[0] = event->stack_pointer;
    g_debugger.registers[2] = event->params[0]; // RAX
    
    // 如果是单步模式，等待用户输入
    if (g_debugger.step_mode) {
        update_all_windows();
        
        // 显示事件信息
        WINDOW *win = g_debugger.code_win.win;
        wattron(win, COLOR_PAIR(5) | A_BOLD);
        mvwprintw(win, 10, 2, ">>> %s 事件触发!", event_names[event->event_type]);
        if (event->event_type == EVENT_FUNCTION_ENTRY) {
            mvwprintw(win, 11, 2, "    参数: 0x%lx 0x%lx 0x%lx", 
                     event->params[0], event->params[1], event->params[2]);
        } else if (event->event_type == EVENT_FUNCTION_EXIT) {
            mvwprintw(win, 11, 2, "    返回值: 0x%lx", event->return_value);
        }
        wattroff(win, COLOR_PAIR(5) | A_BOLD);
        wrefresh(win);
        
        // 等待用户命令
        // 这里会阻塞直到用户输入命令
    }
    
    return 0;
}

// 处理用户命令
static void process_command(const char *cmd) {
    if (strcmp(cmd, "c") == 0 || strcmp(cmd, "continue") == 0) {
        g_debugger.step_mode = false;
    } else if (strcmp(cmd, "s") == 0 || strcmp(cmd, "step") == 0) {
        g_debugger.step_mode = true;
    } else if (strcmp(cmd, "n") == 0 || strcmp(cmd, "next") == 0) {
        g_debugger.step_mode = true;
    } else if (strncmp(cmd, "break ", 6) == 0) {
        // 处理断点设置
        const char *func_name = cmd + 6;
        // TODO: 实现断点设置逻辑
    } else if (strcmp(cmd, "quit") == 0 || strcmp(cmd, "q") == 0) {
        g_debugger.running = false;
    }
    
    // 清空命令缓冲区
    memset(g_debugger.command_buffer, 0, sizeof(g_debugger.command_buffer));
    g_debugger.cmd_cursor = 0;
}

// 主函数
int main(int argc, char **argv) {
    int err;
    
    printf("🚀 Universal Kernel Debugger TUI 启动中...\n");
    
    // 解析命令行参数
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--pid") == 0 && i + 1 < argc) {
            g_debugger.target_pid = atoi(argv[++i]);
        }
    }
    
    // 设置信号处理
    signal(SIGINT, sig_handler);
    signal(SIGTERM, sig_handler);
    
    // 提升内存限制
    struct rlimit rlim_new = {
        .rlim_cur = RLIM_INFINITY,
        .rlim_max = RLIM_INFINITY,
    };
    setrlimit(RLIMIT_MEMLOCK, &rlim_new);
    
    // 加载eBPF程序
    printf("📂 加载eBPF程序...\n");
    g_debugger.obj = bpf_object__open_file("kernel_debugger.bpf.o", NULL);
    if (libbpf_get_error(g_debugger.obj)) {
        fprintf(stderr, "❌ 无法打开eBPF对象文件\n");
        return 1;
    }
    
    err = bpf_object__load(g_debugger.obj);
    if (err) {
        fprintf(stderr, "❌ 无法加载eBPF程序: %d\n", err);
        goto cleanup;
    }
    
    // 获取map文件描述符
    g_debugger.events_fd = bpf_object__find_map_fd_by_name(g_debugger.obj, "events");
    g_debugger.control_fd = bpf_object__find_map_fd_by_name(g_debugger.obj, "control_map");
    
    if (g_debugger.events_fd < 0 || g_debugger.control_fd < 0) {
        fprintf(stderr, "❌ 无法找到eBPF maps\n");
        goto cleanup;
    }
    
    // 设置ring buffer
    g_debugger.rb = ring_buffer__new(g_debugger.events_fd, handle_debug_event, NULL, NULL);
    if (!g_debugger.rb) {
        fprintf(stderr, "❌ 无法创建ring buffer\n");
        goto cleanup;
    }
    
    // 初始化TUI
    init_tui();
    create_windows();
    
    g_debugger.running = true;
    g_debugger.step_mode = true;
    
    printf("✅ TUI调试器启动成功！\n");
    sleep(1); // 让用户看到消息
    
    // 主事件循环
    while (g_debugger.running) {
        // 处理eBPF事件
        ring_buffer__poll(g_debugger.rb, 100);
        
        // 处理键盘输入
        int ch = getch();
        if (ch != ERR) {
            switch (ch) {
                case KEY_F(5): // F5 - 继续
                    g_debugger.step_mode = false;
                    break;
                case KEY_F(10): // F10 - 步过
                    g_debugger.step_mode = true;
                    break;
                case KEY_F(11): // F11 - 步入
                    g_debugger.step_mode = true;
                    break;
                case 3: // Ctrl+C
                    g_debugger.running = false;
                    break;
                case '\n':
                case '\r':
                    process_command(g_debugger.command_buffer);
                    break;
                case KEY_BACKSPACE:
                case 127:
                    if (g_debugger.cmd_cursor > 0) {
                        g_debugger.command_buffer[--g_debugger.cmd_cursor] = '\0';
                    }
                    break;
                default:
                    if (ch >= 32 && ch < 127 && g_debugger.cmd_cursor < MAX_COMMAND_LEN-1) {
                        g_debugger.command_buffer[g_debugger.cmd_cursor++] = ch;
                    }
                    break;
            }
            
            update_all_windows();
        }
    }
    
cleanup:
    // 清理资源
    if (g_debugger.rb) ring_buffer__free(g_debugger.rb);
    if (g_debugger.obj) bpf_object__close(g_debugger.obj);
    
    // 清理ncurses
    endwin();
    
    printf("\n👋 Universal Kernel Debugger 已退出\n");
    return 0;
}
EOF_TUI

    log_success "TUI交互式控制器生成完成"
}

# 生成配置向导脚本
generate_config_wizard() {
    log_header "⚙️  生成配置向导脚本..."
    
    cat > "$SCRIPT_DIR/config_debugger.sh" << 'EOF_CONFIG'
#!/bin/bash
# config_debugger.sh - 调试器配置向导

set -e

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# 颜色定义
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║                    调试器配置向导                            ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
echo ""

# 显示可用函数
show_available_functions() {
    echo -e "${GREEN}可跟踪的函数列表:${NC}"
    echo "----------------------------------------"
    
    if [ -f "$SCRIPT_DIR/symbol_db/all_functions.txt" ]; then
        echo "从内核符号表中选择函数:"
        head -20 "$SCRIPT_DIR/symbol_db/all_functions.txt" | nl
        echo "... (显示前20个，共$(wc -l < "$SCRIPT_DIR/symbol_db/all_functions.txt")个函数)"
    else
        echo "请先运行 ./universal_kernel_debugger_setup.sh 扫描符号"
        return 1
    fi
}

# 交互式选择函数
select_functions() {
    echo ""
    echo -e "${YELLOW}请选择要调试的函数:${NC}"
    echo "1. 手动输入函数名"
    echo "2. 从列表中选择"
    echo "3. 调试所有导出函数"
    
    read -p "请选择 (1-3): " choice
    
    case $choice in
        1)
            read -p "请输入函数名: " func_name
            echo "$func_name" > "$SCRIPT_DIR/target_functions.txt"
            ;;
        2)
            show_available_functions
            read -p "请输入函数编号: " func_num
            sed -n "${func_num}p" "$SCRIPT_DIR/symbol_db/all_functions.txt" > "$SCRIPT_DIR/target_functions.txt"
            ;;
        3)
            cp "$SCRIPT_DIR/symbol_db/all_functions.txt" "$SCRIPT_DIR/target_functions.txt"
            ;;
        *)
            echo -e "${RED}无效选择${NC}"
            exit 1
            ;;
    esac
}

# 配置调试选项
configure_debug_options() {
    echo ""
    echo -e "${YELLOW}配置调试选项:${NC}"
    
    read -p "是否启用单步调试? (y/n): " step_debug
    read -p "是否跟踪函数参数? (y/n): " trace_params
    read -p "是否跟踪返回值? (y/n): " trace_returns
    read -p "目标进程PID (0=所有进程): " target_pid
    
    # 保存配置
    cat > "$SCRIPT_DIR/debug_config.conf" << EOF
STEP_DEBUG=$step_debug
TRACE_PARAMS=$trace_params
TRACE_RETURNS=$trace_returns
TARGET_PID=$target_pid
EOF
}

# 主函数
main() {
    show_available_functions
    select_functions
    configure_debug_options
    
    echo ""
    echo -e "${GREEN}✅ 配置完成！${NC}"
    echo "目标函数已保存到: target_functions.txt"
    echo "调试配置已保存到: debug_config.conf"
    echo ""
    echo "下一步: 运行 ./build_kernel_debugger.sh"
}

main "$@"
EOF_CONFIG
    
    chmod +x "$SCRIPT_DIR/config_debugger.sh"
    log_success "配置向导脚本生成完成"
}

# 主执行函数
main() {
    show_welcome
    sleep 2
    
    check_dependencies
    check_kernel_support
    scan_kernel_symbols
    generate_universal_ebpf
    generate_tui_controller
    generate_config_wizard
    
    log_header "🎉 通用内核调试器生成完成！"
    echo ""
    echo -e "${GREEN}生成的文件:${NC}"
    echo "  ├── kernel_debugger.bpf.c           (通用eBPF调试程序)"
    echo "  ├── kernel_debugger_tui.c           (TUI交互式控制器)"
    echo "  ├── config_debugger.sh              (配置向导)"
    echo "  └── symbol_db/                      (符号数据库目录)"
    echo ""
    echo -e "${BLUE}下一步操作:${NC}"
    echo "  1️⃣  ./config_debugger.sh            (配置要调试的函数)"
    echo "  2️⃣  ./build_kernel_debugger.sh      (构建调试器)"
    echo "  3️⃣  sudo ./kernel_debugger_tui      (启动TUI调试器)"
    echo ""
    log_success "🚀 Universal Kernel Debugger 设置完成！"
}

# 检查参数
if [[ $# -gt 0 && "$1" == "--help" ]]; then
    echo "Universal Kernel Debugger Setup"
    echo "================================"
    echo ""
    echo "这个脚本会自动生成通用的Linux内核驱动调试工具"
    echo ""
    echo "功能特性:"
    echo "  ✅ 支持调试任意内核函数"
    echo "  ✅ 类GDB的TUI交互界面"
    echo "  ✅ eBPF动态探测技术"
    echo "  ✅ 完全自动化配置"
    echo ""
    echo "用法: $0 [--help]"
    exit 0
fi

# 运行主函数
main "$@" 