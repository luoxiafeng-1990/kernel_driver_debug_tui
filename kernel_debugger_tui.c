/*
 * Universal Kernel Debugger - TUI Version
 * Target: RISC-V 64-bit Linux with TacoSys Driver
 * Features: Real-time kernel debugging with eBPF integration
 */
#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <errno.h>
#include <fcntl.h>
#include <sys/types.h>
#include <sys/stat.h>
#include <sys/mman.h>
#include <linux/perf_event.h>
#include <linux/bpf.h>
#include <sys/syscall.h>
#include <ncurses.h>
#include <panel.h>
#include <pthread.h>
#include <time.h>
#include <locale.h>
#include <wchar.h>

// BPF系统调用包装
static inline int bpf(int cmd, union bpf_attr *attr, unsigned int size)
{
    return syscall(__NR_bpf, cmd, attr, size);
}

// 调试器状态
typedef enum {
    DEBUG_STOPPED,
    DEBUG_RUNNING,
    DEBUG_STEPPING,
    DEBUG_BREAKPOINT
} debug_state_t;

// 断点信息
typedef struct breakpoint {
    unsigned long addr;
    int enabled;
    char symbol[64];
    struct breakpoint *next;
} breakpoint_t;

// 寄存器信息 (RISC-V)
typedef struct {
    unsigned long pc;      // 程序计数器
    unsigned long ra;      // 返回地址
    unsigned long sp;      // 栈指针
    unsigned long gp;      // 全局指针
    unsigned long tp;      // 线程指针
    unsigned long t0, t1, t2;  // 临时寄存器
    unsigned long s0, s1;      // 保存寄存器
    unsigned long a0, a1, a2, a3, a4, a5, a6, a7;  // 参数寄存器
    unsigned long s2, s3, s4, s5, s6, s7, s8, s9, s10, s11;  // 保存寄存器
    unsigned long t3, t4, t5, t6;  // 临时寄存器
} riscv_regs_t;

// 窗口焦点定义
typedef enum {
    FOCUS_REGISTERS = 0,
    FOCUS_VARIABLES,
    FOCUS_STACK,
    FOCUS_CODE,
    FOCUS_MEMORY,
    FOCUS_COMMAND,
    FOCUS_COUNT
} window_focus_t;

// 调试器上下文
typedef struct {
    debug_state_t state;
    int bpf_fd;
    int perf_fd;
    void *perf_mmap;
    size_t perf_mmap_size;
    int bpf_loaded;  // BPF程序加载状态
    
    // UI窗口
    WINDOW *main_win;
    WINDOW *reg_win;        // 寄存器窗口
    WINDOW *var_win;        // 变量窗口
    WINDOW *stack_win;      // 函数调用堆栈窗口
    WINDOW *mem_win;        // 内存窗口
    WINDOW *code_win;       // 代码视图窗口
    WINDOW *cmd_win;        // 命令窗口
    WINDOW *status_win;     // 状态栏窗口
    
    // 面板
    PANEL *main_panel;
    PANEL *reg_panel;
    PANEL *var_panel;
    PANEL *stack_panel;
    PANEL *mem_panel;
    PANEL *code_panel;
    PANEL *cmd_panel;
    PANEL *status_panel;
    
    // 调试数据
    riscv_regs_t regs;
    breakpoint_t *breakpoints;
    char current_function[128];
    unsigned long current_addr;
    
    // 窗口滚动位置
    int reg_scroll_pos;
    int var_scroll_pos;
    int stack_scroll_pos;
    int code_scroll_pos;
    int mem_scroll_pos;
    
    // 控制标志
    int running;
    int mouse_enabled;
    window_focus_t current_focus;  // 当前焦点窗口
    pthread_t event_thread;
    pthread_mutex_t data_mutex;
    
} debugger_ctx_t;

static debugger_ctx_t *g_ctx = NULL;

// 颜色对定义
#define COLOR_TITLE     1
#define COLOR_BORDER    2
#define COLOR_HIGHLIGHT 3
#define COLOR_ERROR     4
#define COLOR_SUCCESS   5
#define COLOR_WARNING   6
#define COLOR_INFO      7
#define COLOR_FOCUSED   8    // 焦点窗口边框颜色

// 窗口布局定义
#define MAIN_HEIGHT     (LINES - 2)
#define MAIN_WIDTH      COLS
#define LEFT_WIDTH      30      // 左侧窗口宽度（寄存器、变量、堆栈）
#define MEM_WIDTH       40      // 内存窗口宽度
#define CMD_HEIGHT      8       // 命令窗口高度
#define STATUS_HEIGHT   2       // 状态栏高度

// 信号处理
static void signal_handler(int sig)
{
    if (g_ctx) {
        g_ctx->running = 0;
    }
    endwin();
    exit(0);
}

// 初始化颜色
static void init_colors(void)
{
    start_color();
    init_pair(COLOR_TITLE,     COLOR_YELLOW, COLOR_BLUE);
    init_pair(COLOR_BORDER,    COLOR_CYAN,   COLOR_BLACK);
    init_pair(COLOR_HIGHLIGHT, COLOR_BLACK,  COLOR_YELLOW);
    init_pair(COLOR_ERROR,     COLOR_WHITE,  COLOR_RED);
    init_pair(COLOR_SUCCESS,   COLOR_WHITE,  COLOR_GREEN);
    init_pair(COLOR_WARNING,   COLOR_BLACK,  COLOR_YELLOW);
    init_pair(COLOR_INFO,      COLOR_WHITE,  COLOR_BLUE);
    init_pair(COLOR_FOCUSED,   COLOR_YELLOW, COLOR_BLACK);  // 焦点窗口高亮边框
}

// 创建带边框的窗口
static WINDOW *create_bordered_window(int height, int width, int y, int x, const wchar_t *title)
{
    WINDOW *win = newwin(height, width, y, x);
    if (!win) return NULL;
    
    box(win, 0, 0);
    if (title) {
        wattron(win, COLOR_PAIR(COLOR_TITLE) | A_BOLD);
        mvwaddwstr(win, 0, 2, L" ");
        waddwstr(win, title);
        waddwstr(win, L" ");
        wattroff(win, COLOR_PAIR(COLOR_TITLE) | A_BOLD);
    }
    
    wattron(win, COLOR_PAIR(COLOR_BORDER));
    wrefresh(win);
    wattroff(win, COLOR_PAIR(COLOR_BORDER));
    
    return win;
}

// 更新窗口边框以显示焦点状态
static void update_window_border(WINDOW *win, const wchar_t *title, int is_focused)
{
    int border_color = is_focused ? COLOR_FOCUSED : COLOR_BORDER;
    
    wattron(win, COLOR_PAIR(border_color) | (is_focused ? A_BOLD : A_NORMAL));
    box(win, 0, 0);
    wattroff(win, COLOR_PAIR(border_color) | (is_focused ? A_BOLD : A_NORMAL));
    
    if (title) {
        int title_color = is_focused ? COLOR_FOCUSED : COLOR_TITLE;
        wattron(win, COLOR_PAIR(title_color) | A_BOLD);
        mvwaddwstr(win, 0, 2, L" ");
        waddwstr(win, title);
        waddwstr(win, L" ");
        wattroff(win, COLOR_PAIR(title_color) | A_BOLD);
    }
}

// 移动鼠标光标到指定窗口中心
static void move_cursor_to_window(WINDOW *win)
{
    if (!win) return;
    
    int height, width;
    getmaxyx(win, height, width);
    
    // 移动光标到窗口中心
    wmove(win, height / 2, width / 2);
    wrefresh(win);
}

// 检查鼠标是否在指定窗口内
static int is_mouse_in_window(WINDOW *win, int mouse_y, int mouse_x)
{
    if (!win) return 0;
    
    int win_y, win_x, win_h, win_w;
    getbegyx(win, win_y, win_x);
    getmaxyx(win, win_h, win_w);
    
    return (mouse_y >= win_y && mouse_y < win_y + win_h &&
            mouse_x >= win_x && mouse_x < win_x + win_w);
}

// 处理窗口滚动
static void handle_window_scroll(debugger_ctx_t *ctx, int direction)
{
    switch (ctx->current_focus) {
        case FOCUS_REGISTERS:
            ctx->reg_scroll_pos += direction;
            if (ctx->reg_scroll_pos < 0) ctx->reg_scroll_pos = 0;
            break;
        case FOCUS_VARIABLES:
            ctx->var_scroll_pos += direction;
            if (ctx->var_scroll_pos < 0) ctx->var_scroll_pos = 0;
            break;
        case FOCUS_STACK:
            ctx->stack_scroll_pos += direction;
            if (ctx->stack_scroll_pos < 0) ctx->stack_scroll_pos = 0;
            break;
        case FOCUS_CODE:
            ctx->code_scroll_pos += direction;
            if (ctx->code_scroll_pos < 0) ctx->code_scroll_pos = 0;
            break;
        case FOCUS_MEMORY:
            ctx->mem_scroll_pos += direction;
            if (ctx->mem_scroll_pos < 0) ctx->mem_scroll_pos = 0;
            break;
        default:
            break;
    }
}

// 切换窗口焦点
static void switch_window_focus(debugger_ctx_t *ctx, window_focus_t new_focus)
{
    if (new_focus >= FOCUS_COUNT) return;
    
    ctx->current_focus = new_focus;
    
    // 移动鼠标光标到新的焦点窗口
    switch (new_focus) {
        case FOCUS_REGISTERS:
            move_cursor_to_window(ctx->reg_win);
            break;
        case FOCUS_VARIABLES:
            move_cursor_to_window(ctx->var_win);
            break;
        case FOCUS_STACK:
            move_cursor_to_window(ctx->stack_win);
            break;
        case FOCUS_CODE:
            move_cursor_to_window(ctx->code_win);
            break;
        case FOCUS_MEMORY:
            move_cursor_to_window(ctx->mem_win);
            break;
        case FOCUS_COMMAND:
            move_cursor_to_window(ctx->cmd_win);
            break;
        default:
            break;
    }
}

// 初始化UI
static int init_ui(debugger_ctx_t *ctx)
{
    // 设置locale以支持UTF-8
    setlocale(LC_ALL, "");
    
    // 设置环境变量确保UTF-8编码
    setenv("LC_ALL", "C.UTF-8", 1);
    setenv("LANG", "C.UTF-8", 1);
    
    // 初始化ncurses
    initscr();
    if (!has_colors()) {
        endwin();
        fprintf(stderr, "终端不支持颜色\n");
        return -1;
    }
    
    init_colors();
    cbreak();
    noecho();
    keypad(stdscr, TRUE);
    
    // 启用UTF-8支持
    if (setlocale(LC_CTYPE, "") == NULL) {
        setlocale(LC_CTYPE, "C.UTF-8");
    }
    
    // 启用鼠标支持
    if (mousemask(ALL_MOUSE_EVENTS, NULL) != 0) {
        ctx->mouse_enabled = 1;
    }
    
    // 创建主窗口布局
    int content_height = MAIN_HEIGHT - CMD_HEIGHT - STATUS_HEIGHT;  // 主内容区域高度
    int code_width = MAIN_WIDTH - LEFT_WIDTH - MEM_WIDTH;           // 代码窗口宽度
    int left_win_height = content_height / 3;                       // 左侧每个窗口高度
    
    // 状态栏 (顶部)
    ctx->status_win = create_bordered_window(STATUS_HEIGHT, MAIN_WIDTH, 0, 0, L"状态");
    
    // 左侧窗口 - 寄存器 (左上)
    ctx->reg_win = create_bordered_window(left_win_height, LEFT_WIDTH, STATUS_HEIGHT, 0, L"寄存器");
    
    // 左侧窗口 - 变量 (左中)
    ctx->var_win = create_bordered_window(left_win_height, LEFT_WIDTH, STATUS_HEIGHT + left_win_height, 0, L"变量");
    
    // 左侧窗口 - 函数调用堆栈 (左下)
    ctx->stack_win = create_bordered_window(content_height - 2 * left_win_height, LEFT_WIDTH, STATUS_HEIGHT + 2 * left_win_height, 0, L"函数调用堆栈");
    
    // 内存窗口 (右侧)
    ctx->mem_win = create_bordered_window(content_height, MEM_WIDTH, STATUS_HEIGHT, MAIN_WIDTH - MEM_WIDTH, L"内存");
    
    // 代码窗口 (中央)
    ctx->code_win = create_bordered_window(content_height, code_width, STATUS_HEIGHT, LEFT_WIDTH, L"代码视图");
    
    // 命令窗口 (底部)
    ctx->cmd_win = create_bordered_window(CMD_HEIGHT, MAIN_WIDTH, MAIN_HEIGHT - CMD_HEIGHT + STATUS_HEIGHT, 0, L"命令");
    
    // 创建面板
    ctx->status_panel = new_panel(ctx->status_win);
    ctx->reg_panel = new_panel(ctx->reg_win);
    ctx->var_panel = new_panel(ctx->var_win);
    ctx->stack_panel = new_panel(ctx->stack_win);
    ctx->mem_panel = new_panel(ctx->mem_win);
    ctx->code_panel = new_panel(ctx->code_win);
    ctx->cmd_panel = new_panel(ctx->cmd_win);
    
    update_panels();
    doupdate();
    
    // 设置初始焦点
    switch_window_focus(ctx, ctx->current_focus);
    
    return 0;
}

// 清理UI
static void cleanup_ui(debugger_ctx_t *ctx)
{
    if (ctx->status_panel) del_panel(ctx->status_panel);
    if (ctx->reg_panel) del_panel(ctx->reg_panel);
    if (ctx->var_panel) del_panel(ctx->var_panel);
    if (ctx->stack_panel) del_panel(ctx->stack_panel);
    if (ctx->mem_panel) del_panel(ctx->mem_panel);
    if (ctx->code_panel) del_panel(ctx->code_panel);
    if (ctx->cmd_panel) del_panel(ctx->cmd_panel);
    
    if (ctx->status_win) delwin(ctx->status_win);
    if (ctx->reg_win) delwin(ctx->reg_win);
    if (ctx->var_win) delwin(ctx->var_win);
    if (ctx->stack_win) delwin(ctx->stack_win);
    if (ctx->mem_win) delwin(ctx->mem_win);
    if (ctx->code_win) delwin(ctx->code_win);
    if (ctx->cmd_win) delwin(ctx->cmd_win);
    
    endwin();
}

// 更新状态栏
static void update_status(debugger_ctx_t *ctx)
{
    werase(ctx->status_win);
    box(ctx->status_win, 0, 0);
    
    wattron(ctx->status_win, COLOR_PAIR(COLOR_TITLE) | A_BOLD);
    wattroff(ctx->status_win, COLOR_PAIR(COLOR_TITLE) | A_BOLD);
    
    // 状态信息
    const wchar_t *state_str;
    int state_color;
    switch (ctx->state) {
        case DEBUG_STOPPED:
            state_str = L"已停止";
            state_color = COLOR_ERROR;
            break;
        case DEBUG_RUNNING:
            state_str = L"运行中";
            state_color = COLOR_SUCCESS;
            break;
        case DEBUG_STEPPING:
            state_str = L"单步执行";
            state_color = COLOR_WARNING;
            break;
        case DEBUG_BREAKPOINT:
            state_str = L"断点";
            state_color = COLOR_INFO;
            break;
        default:
            state_str = L"未知";
            state_color = COLOR_ERROR;
    }
    
    wattron(ctx->status_win, COLOR_PAIR(state_color));
    mvwaddwstr(ctx->status_win, 1, 2, L"状态: ");
    waddwstr(ctx->status_win, state_str);
    wattroff(ctx->status_win, COLOR_PAIR(state_color));
    
    // 显示BPF程序加载状态
    if (ctx->bpf_loaded) {
        wattron(ctx->status_win, COLOR_PAIR(COLOR_SUCCESS));
        mvwaddwstr(ctx->status_win, 1, 18, L"BPF: ✓");
        wattroff(ctx->status_win, COLOR_PAIR(COLOR_SUCCESS));
    } else {
        wattron(ctx->status_win, COLOR_PAIR(COLOR_WARNING));
        mvwaddwstr(ctx->status_win, 1, 18, L"BPF: ✗");
        wattroff(ctx->status_win, COLOR_PAIR(COLOR_WARNING));
    }
    
    mvwaddwstr(ctx->status_win, 1, 28, L"函数: ");
    mvwprintw(ctx->status_win, 1, 34, "%s", ctx->current_function);
    mvwaddwstr(ctx->status_win, 1, 58, L"地址: ");
    mvwprintw(ctx->status_win, 1, 64, "0x%lx", ctx->current_addr);
    
    time_t now = time(NULL);
    char timestr[32];
    strftime(timestr, sizeof(timestr), "%H:%M:%S", localtime(&now));
    mvwprintw(ctx->status_win, 1, MAIN_WIDTH - 12, "%s", timestr);
    
    wrefresh(ctx->status_win);
}

// 更新寄存器窗口
static void update_registers(debugger_ctx_t *ctx)
{
    werase(ctx->reg_win);
    update_window_border(ctx->reg_win, L"寄存器", ctx->current_focus == FOCUS_REGISTERS);
    
    int y = 2;
    int height = getmaxy(ctx->reg_win) - 3;
    int start_line = ctx->reg_scroll_pos;
    
    // 所有寄存器数据
    struct {
        const char *name;
        unsigned long value;
    } registers[] = {
        {"PC", ctx->regs.pc}, {"RA", ctx->regs.ra}, {"SP", ctx->regs.sp},
        {"GP", ctx->regs.gp}, {"TP", ctx->regs.tp}, {"", 0},
        {"T0", ctx->regs.t0}, {"T1", ctx->regs.t1}, {"T2", ctx->regs.t2}, {"", 0},
        {"S0", ctx->regs.s0}, {"S1", ctx->regs.s1}, {"", 0},
        {"A0", ctx->regs.a0}, {"A1", ctx->regs.a1}, {"A2", ctx->regs.a2}, {"A3", ctx->regs.a3},
        {"A4", ctx->regs.a4}, {"A5", ctx->regs.a5}, {"A6", ctx->regs.a6}, {"A7", ctx->regs.a7},
        {"", 0},
        {"S2", ctx->regs.s2}, {"S3", ctx->regs.s3}, {"S4", ctx->regs.s4}, {"S5", ctx->regs.s5},
        {"S6", ctx->regs.s6}, {"S7", ctx->regs.s7}, {"S8", ctx->regs.s8}, {"S9", ctx->regs.s9},
        {"S10", ctx->regs.s10}, {"S11", ctx->regs.s11}, {"", 0},
        {"T3", ctx->regs.t3}, {"T4", ctx->regs.t4}, {"T5", ctx->regs.t5}, {"T6", ctx->regs.t6}
    };
    
    int total_lines = sizeof(registers) / sizeof(registers[0]);
    
    for (int i = start_line; i < total_lines && y < height + 1; i++) {
        if (strlen(registers[i].name) == 0) {
            y++;  // 空行
        } else {
            mvwprintw(ctx->reg_win, y++, 2, "%-3s: 0x%016lx", 
                     registers[i].name, registers[i].value);
        }
    }
    
    // 显示滚动指示器
    if (start_line > 0 || start_line + height < total_lines) {
        wattron(ctx->reg_win, COLOR_PAIR(COLOR_INFO));
        mvwprintw(ctx->reg_win, height + 1, 2, "[%d/%d]", start_line + 1, total_lines);
        wattroff(ctx->reg_win, COLOR_PAIR(COLOR_INFO));
    }
    
    wrefresh(ctx->reg_win);
}

// 更新变量窗口
static void update_variables(debugger_ctx_t *ctx)
{
    werase(ctx->var_win);
    update_window_border(ctx->var_win, L"变量", ctx->current_focus == FOCUS_VARIABLES);
    
    int y = 2;
    int height = getmaxy(ctx->var_win) - 3;
    int start_line = ctx->var_scroll_pos;
    
    // 所有变量数据
    struct {
        const char *type;  // "header", "local", "global", "empty"
        const char *name;
        const char *value;
        const char *datatype;
    } all_vars[] = {
        {"header", "局部变量:", "", ""},
        {"local", "ctx", "0x7fff1234", "debugger_ctx_t*"},
        {"local", "fd", "3", "int"},
        {"local", "ret", "-1", "int"},
        {"local", "buf", "0x7fff5678", "char[256]"},
        {"local", "size", "256", "size_t"},
        {"local", "i", "0", "int"},
        {"local", "addr", "0x400000", "unsigned long"},
        {"empty", "", "", ""},
        {"header", "全局变量:", "", ""},
        {"global", "g_ctx", "0x601020", "debugger_ctx_t*"},
        {"global", "debug_level", "2", "int"},
        {"global", "max_breakpoints", "32", "int"},
        {"global", "log_file", "0x602030", "FILE*"},
        {"global", "config_path", "\"/etc/debug.conf\"", "char*"}
    };
    
    int total_lines = sizeof(all_vars) / sizeof(all_vars[0]);
    
    for (int i = start_line; i < total_lines && y < height + 1; i++) {
        if (strcmp(all_vars[i].type, "empty") == 0) {
            y++;  // 空行
        } else if (strcmp(all_vars[i].type, "header") == 0) {
            wattron(ctx->var_win, COLOR_PAIR(COLOR_INFO) | A_BOLD);
            mvwaddwstr(ctx->var_win, y++, 2, (wchar_t*)all_vars[i].name);
            wattroff(ctx->var_win, COLOR_PAIR(COLOR_INFO) | A_BOLD);
        } else {
            mvwprintw(ctx->var_win, y++, 4, "%-8s %-12s %s", 
                     all_vars[i].name, all_vars[i].datatype, all_vars[i].value);
        }
    }
    
    // 显示滚动指示器
    if (start_line > 0 || start_line + height < total_lines) {
        wattron(ctx->var_win, COLOR_PAIR(COLOR_INFO));
        mvwprintw(ctx->var_win, height + 1, 2, "[%d/%d]", start_line + 1, total_lines);
        wattroff(ctx->var_win, COLOR_PAIR(COLOR_INFO));
    }
    
    wrefresh(ctx->var_win);
}

// 更新内存窗口
static void update_memory(debugger_ctx_t *ctx)
{
    werase(ctx->mem_win);
    update_window_border(ctx->mem_win, L"内存", ctx->current_focus == FOCUS_MEMORY);
    
    // 显示内存内容 (模拟)
    unsigned long base_addr = ctx->current_addr & ~0xF;
    int y = 2;
    
    for (int i = 0; i < 10; i++) {
        unsigned long addr = base_addr + (i * 16);
        mvwprintw(ctx->mem_win, y++, 2, "%016lx:", addr);
        
        // 模拟内存数据
        for (int j = 0; j < 16; j += 4) {
            unsigned int data = (addr + j) & 0xFFFFFFFF;
            mvwprintw(ctx->mem_win, y-1, 20 + j*2, "%08x ", data);
        }
    }
    
    wrefresh(ctx->mem_win);
}

// 更新调用栈窗口
static void update_stack(debugger_ctx_t *ctx)
{
    werase(ctx->stack_win);
    update_window_border(ctx->stack_win, L"函数调用堆栈", ctx->current_focus == FOCUS_STACK);
    
    int y = 2;
    int height = getmaxy(ctx->stack_win) - 3;
    int start_frame = ctx->stack_scroll_pos;
    
    // 模拟完整的函数调用堆栈
    const char *stack_frames[][3] = {
        {"0", ctx->current_function, "kernel_debugger_tui.c:156"},
        {"1", "taco_sys_mmz_alloc", "taco_sys_mmz.c:89"},
        {"2", "taco_sys_init", "taco_sys_init.c:45"},
        {"3", "module_init", "taco_sys_module.c:23"},
        {"4", "kernel_init", "init/main.c:1234"},
        {"5", "kernel_thread", "kernel/kthread.c:567"},
        {"6", "ret_from_fork", "arch/riscv/kernel/entry.S:123"},
        {"7", "start_kernel", "init/main.c:890"},
        {"8", "early_init", "arch/riscv/kernel/setup.c:234"},
        {"9", "setup_arch", "arch/riscv/kernel/setup.c:156"}
    };
    
    int frame_count = sizeof(stack_frames) / sizeof(stack_frames[0]);
    
    for (int i = start_frame; i < frame_count && y < height - 1; i++) {
        // 高亮当前栈帧
        if (i == 0) {
            wattron(ctx->stack_win, COLOR_PAIR(COLOR_HIGHLIGHT) | A_BOLD);
        }
        
        // 显示栈帧编号和函数名
        mvwprintw(ctx->stack_win, y++, 2, "#%-2s %s", stack_frames[i][0], stack_frames[i][1]);
        
        if (i == 0) {
            wattroff(ctx->stack_win, COLOR_PAIR(COLOR_HIGHLIGHT) | A_BOLD);
        }
        
        // 显示地址和源文件信息
        if (y < height - 1) {
            wattron(ctx->stack_win, COLOR_PAIR(COLOR_INFO));
            if (i == 0) {
                mvwprintw(ctx->stack_win, y++, 4, "0x%lx %s", ctx->current_addr, stack_frames[i][2]);
            } else {
                mvwprintw(ctx->stack_win, y++, 4, "0x%lx %s", ctx->current_addr - (i * 0x100), stack_frames[i][2]);
            }
            wattroff(ctx->stack_win, COLOR_PAIR(COLOR_INFO));
        }
        
        // 添加间隔
        if (i < frame_count - 1 && y < height - 1) {
            y++;
        }
    }
    
    wrefresh(ctx->stack_win);
}

// 更新代码窗口
static void update_code(debugger_ctx_t *ctx)
{
    werase(ctx->code_win);
    update_window_border(ctx->code_win, L"代码视图", ctx->current_focus == FOCUS_CODE);
    
    int y = 2;
    int height = getmaxy(ctx->code_win) - 3;
    int width = getmaxx(ctx->code_win) - 4;
    
    // 显示反汇编代码 (模拟)
    unsigned long base_addr = ctx->current_addr - ((height/2 - ctx->code_scroll_pos) * 4);
    int current_line = height/2 + 1 + ctx->code_scroll_pos;  // 当前执行行号
    
    for (int i = 0; i < height; i++) {
        unsigned long addr = base_addr + (i * 4);
        int line_num = current_line - height/2 + i;
        int is_current = (addr == ctx->current_addr);
        
        // 显示行号
        if (is_current) {
            wattron(ctx->code_win, COLOR_PAIR(COLOR_HIGHLIGHT) | A_BOLD);
            mvwprintw(ctx->code_win, y, 2, "%3d=> ", line_num);
        } else {
            wattron(ctx->code_win, COLOR_PAIR(COLOR_INFO));
            mvwprintw(ctx->code_win, y, 2, "%3d:  ", line_num);
            wattroff(ctx->code_win, COLOR_PAIR(COLOR_INFO));
        }
        
        // 模拟RISC-V指令
        const char *instructions[] = {
            "addi sp, sp, -32",
            "sd   ra, 24(sp)", 
            "sd   s0, 16(sp)",
            "addi s0, sp, 32",
            "li   a0, 0x1000",
            "call taco_sys_mmz_alloc",
            "mv   s1, a0",
            "beqz s1, .error",
            "li   a1, 64",
            "mv   a0, s1", 
            "call memset",
            "ld   ra, 24(sp)",
            "ld   s0, 16(sp)",
            "addi sp, sp, 32",
            "ret"
        };
        
        int inst_idx = (addr / 4) % (sizeof(instructions) / sizeof(instructions[0]));
        
        // 显示地址和指令，确保不超出窗口宽度
        int remaining_width = width - 8;  // 减去行号和箭头的宽度
        if (remaining_width > 0) {
            char addr_inst[256];
            snprintf(addr_inst, sizeof(addr_inst), "0x%lx: %s", addr, instructions[inst_idx]);
            
            // 截断过长的指令
            if (strlen(addr_inst) > remaining_width) {
                addr_inst[remaining_width - 3] = '.';
                addr_inst[remaining_width - 2] = '.';
                addr_inst[remaining_width - 1] = '.';
                addr_inst[remaining_width] = '\0';
            }
            
            mvwprintw(ctx->code_win, y, 8, "%s", addr_inst);
        }
        
        if (is_current) {
            wattroff(ctx->code_win, COLOR_PAIR(COLOR_HIGHLIGHT) | A_BOLD);
        }
        
        y++;
    }
    
    wrefresh(ctx->code_win);
}

// 更新命令窗口
static void update_command(debugger_ctx_t *ctx)
{
    werase(ctx->cmd_win);
    update_window_border(ctx->cmd_win, L"命令", ctx->current_focus == FOCUS_COMMAND);
    
    int y = 2;
    mvwaddwstr(ctx->cmd_win, y++, 2, L"快捷键:");
    mvwaddwstr(ctx->cmd_win, y++, 2, L"F5-继续  F10-下一步  F11-单步  Tab-切换窗口");
    mvwaddwstr(ctx->cmd_win, y++, 2, L"b-断点   c-继续     s-单步    r-重载BPF  q-退出");
    
    // 显示当前焦点窗口
    const wchar_t *focus_names[] = {
        L"寄存器", L"变量", L"函数调用堆栈", L"代码视图", L"内存", L"命令"
    };
    if (ctx->current_focus < FOCUS_COUNT) {
        wattron(ctx->cmd_win, COLOR_PAIR(COLOR_FOCUSED) | A_BOLD);
        mvwaddwstr(ctx->cmd_win, y++, 2, L"当前焦点: ");
        waddwstr(ctx->cmd_win, focus_names[ctx->current_focus]);
        wattroff(ctx->cmd_win, COLOR_PAIR(COLOR_FOCUSED) | A_BOLD);
    }
    if (!ctx->bpf_loaded) {
        wattron(ctx->cmd_win, COLOR_PAIR(COLOR_WARNING));
        mvwaddwstr(ctx->cmd_win, y++, 2, L"提示: BPF程序未加载，部分功能受限");
        wattroff(ctx->cmd_win, COLOR_PAIR(COLOR_WARNING));
    }
    y++;
    mvwaddwstr(ctx->cmd_win, y++, 2, L"命令: ");
    
    wrefresh(ctx->cmd_win);
}

// 更新所有窗口
static void update_all_windows(debugger_ctx_t *ctx)
{
    pthread_mutex_lock(&ctx->data_mutex);
    
    update_status(ctx);
    update_registers(ctx);
    update_variables(ctx);
    update_stack(ctx);
    update_memory(ctx);
    update_code(ctx);
    update_command(ctx);
    
    update_panels();
    doupdate();
    
    pthread_mutex_unlock(&ctx->data_mutex);
}

// 加载eBPF程序
static int load_bpf_program(debugger_ctx_t *ctx)
{
    ctx->bpf_loaded = 0;  // 初始化为未加载状态
    ctx->bpf_fd = -1;
    
    // 读取eBPF对象文件
    int fd = open("kernel_debugger.bpf.o", O_RDONLY);
    if (fd < 0) {
        // 不打印错误信息，只是标记为未加载
        return -1;
    }
    
    struct stat st;
    if (fstat(fd, &st) < 0) {
        close(fd);
        return -1;
    }
    
    void *obj_buf = mmap(NULL, st.st_size, PROT_READ, MAP_PRIVATE, fd, 0);
    if (obj_buf == MAP_FAILED) {
        close(fd);
        return -1;
    }
    
    close(fd);
    
    // 这里应该解析ELF文件并加载eBPF程序
    // 简化实现，直接创建一个dummy程序
    union bpf_attr attr = {};
    char log_buf[4096];
    
    // 简单的eBPF程序字节码 (返回0)
    struct bpf_insn insns[] = {
        { .code = 0x95, .dst_reg = 0, .src_reg = 0, .off = 0, .imm = 0 }  // exit
    };
    
    attr.prog_type = BPF_PROG_TYPE_KPROBE;
    attr.insn_cnt = sizeof(insns) / sizeof(insns[0]);
    attr.insns = (unsigned long)insns;
    attr.license = (unsigned long)"GPL";
    attr.log_buf = (unsigned long)log_buf;
    attr.log_size = sizeof(log_buf);
    attr.log_level = 1;
    
    ctx->bpf_fd = bpf(BPF_PROG_LOAD, &attr, sizeof(attr));
    if (ctx->bpf_fd < 0) {
        munmap(obj_buf, st.st_size);
        return -1;
    }
    
    munmap(obj_buf, st.st_size);
    ctx->bpf_loaded = 1;  // 标记为已加载
    return 0;
}

// 事件处理线程
static void *event_thread(void *arg)
{
    debugger_ctx_t *ctx = (debugger_ctx_t *)arg;
    
    while (ctx->running) {
        // 模拟事件处理
        usleep(100000);  // 100ms
        
        pthread_mutex_lock(&ctx->data_mutex);
        
        // 更新模拟数据
        ctx->regs.pc += 4;
        ctx->current_addr = ctx->regs.pc;
        
        if (ctx->state == DEBUG_RUNNING) {
            // 模拟程序执行
            static int counter = 0;
            counter++;
            if (counter % 10 == 0) {
                strcpy(ctx->current_function, "taco_sys_mmz_alloc");
            } else if (counter % 7 == 0) {
                strcpy(ctx->current_function, "taco_sys_init");
            }
        }
        
        pthread_mutex_unlock(&ctx->data_mutex);
    }
    
    return NULL;
}

// 处理用户输入
static void handle_input(debugger_ctx_t *ctx)
{
    int ch = getch();
    
    switch (ch) {
        case KEY_F(5):  // F5 - 继续
            ctx->state = DEBUG_RUNNING;
            break;
            
        case KEY_F(10): // F10 - 下一步
            ctx->state = DEBUG_STEPPING;
            break;
            
        case KEY_F(11): // F11 - 单步
            ctx->state = DEBUG_STEPPING;
            break;
            
        case 'q':
        case 'Q':
            ctx->running = 0;
            break;
            
        case 'c':
        case 'C':
            ctx->state = DEBUG_RUNNING;
            break;
            
        case 's':
        case 'S':
            ctx->state = DEBUG_STEPPING;
            break;
            
        case 'b':
        case 'B':
            // 设置断点 (简化实现)
            ctx->state = DEBUG_BREAKPOINT;
            break;
            
        case 'r':
        case 'R':
            // 重新加载BPF程序
            if (ctx->bpf_fd >= 0) {
                close(ctx->bpf_fd);
                ctx->bpf_fd = -1;
            }
            load_bpf_program(ctx);
            break;
            
        case KEY_MOUSE:
            if (ctx->mouse_enabled) {
                MEVENT event;
                if (getmouse(&event) == OK) {
                    // 处理鼠标事件
                }
            }
            break;
            
        case '\t':  // Tab键
            // 切换到下一个窗口
            {
                window_focus_t next_focus = (ctx->current_focus + 1) % FOCUS_COUNT;
                switch_window_focus(ctx, next_focus);
            }
            break;
            
        case KEY_RESIZE:
            // 处理终端大小变化
            endwin();
            refresh();
            clear();
            update_all_windows(ctx);
            break;
    }
}

// 初始化调试器
static int init_debugger(debugger_ctx_t *ctx)
{
    memset(ctx, 0, sizeof(*ctx));
    
    ctx->state = DEBUG_STOPPED;
    ctx->running = 1;
    ctx->current_addr = 0xffffffff80000000UL;  // 内核地址空间
    strcpy(ctx->current_function, "taco_sys_init");
    ctx->bpf_loaded = 0;  // 初始化BPF加载状态
    ctx->bpf_fd = -1;     // 初始化BPF文件描述符
    ctx->current_focus = FOCUS_CODE;  // 默认焦点在代码窗口
    
    // 初始化互斥锁
    if (pthread_mutex_init(&ctx->data_mutex, NULL) != 0) {
        return -1;
    }
    
    // 初始化模拟寄存器
    ctx->regs.pc = ctx->current_addr;
    ctx->regs.sp = 0xffffffff80800000UL;
    ctx->regs.ra = 0xffffffff80000100UL;
    
    return 0;
}

// 清理调试器
static void cleanup_debugger(debugger_ctx_t *ctx)
{
    ctx->running = 0;
    
    if (ctx->event_thread) {
        pthread_join(ctx->event_thread, NULL);
    }
    
    if (ctx->bpf_fd >= 0) {
        close(ctx->bpf_fd);
    }
    
    if (ctx->perf_fd >= 0) {
        close(ctx->perf_fd);
    }
    
    if (ctx->perf_mmap) {
        munmap(ctx->perf_mmap, ctx->perf_mmap_size);
    }
    
    pthread_mutex_destroy(&ctx->data_mutex);
    
    // 清理断点链表
    breakpoint_t *bp = ctx->breakpoints;
    while (bp) {
        breakpoint_t *next = bp->next;
        free(bp);
        bp = next;
    }
}

// 主函数
int main(int argc, char *argv[])
{
    debugger_ctx_t ctx;
 
    // 检查root权限
    if (geteuid() != 0) {
        fprintf(stderr, "❌ 需要root权限运行调试器\n");
        return 1;
    }
    
    // 初始化调试器
    if (init_debugger(&ctx) < 0) {
        fprintf(stderr, "❌ 初始化调试器失败\n");
        return 1;
    }
    
    g_ctx = &ctx;
    
    // 设置信号处理
    signal(SIGINT, signal_handler);
    signal(SIGTERM, signal_handler);
    
    // 初始化UI
    if (init_ui(&ctx) < 0) {
        cleanup_debugger(&ctx);
        return 1;
    }
    
    // 尝试加载eBPF程序 (失败不影响界面启动)
    if (load_bpf_program(&ctx) < 0) {
        // BPF程序加载失败，但继续运行调试器
        // 状态栏会显示BPF未加载的状态
    }
    
    // 启动事件处理线程
    if (pthread_create(&ctx.event_thread, NULL, event_thread, &ctx) != 0) {
        fprintf(stderr, "❌ 创建事件线程失败\n");
        cleanup_ui(&ctx);
        cleanup_debugger(&ctx);
        return 1;
    }
    
    // 主循环
    while (ctx.running) {
        update_all_windows(&ctx);
        handle_input(&ctx);
        usleep(50000);  // 50ms刷新间隔
    }
    
    // 清理
    cleanup_ui(&ctx);
    cleanup_debugger(&ctx);
    
    printf("✅ Universal Kernel Debugger 已退出\n");
    return 0;
} 