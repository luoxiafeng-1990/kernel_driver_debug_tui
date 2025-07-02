// SPDX-License-Identifier: GPL-2.0
// Universal Kernel Debugger - RISC-V eBPF Program

// 完整的内核类型定义（解决头文件依赖问题）
typedef unsigned char __u8;
typedef unsigned short __u16;
typedef unsigned int __u32;
typedef unsigned long long __u64;
typedef signed char __s8;
typedef signed short __s16;
typedef signed int __s32;
typedef signed long long __s64;

// 网络字节序类型
typedef __u16 __be16;
typedef __u32 __be32;
typedef __u64 __be64;
typedef __u32 __wsum;

// 兼容性类型定义
typedef __u8 u8;
typedef __u16 u16;
typedef __u32 u32;
typedef __u64 u64;
typedef __s8 s8;
typedef __s16 s16;
typedef __s32 s32;
typedef __s64 s64;

// BPF常量定义
#define BPF_MAP_TYPE_PERF_EVENT_ARRAY 4
#define BPF_MAP_TYPE_ARRAY 2
#define BPF_F_CURRENT_CPU 0xffffffffULL

#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>

#define MAX_FUNCTION_NAME 64

// 调试事件类型
enum debug_event_type {
    EVENT_FUNCTION_ENTRY = 0,
    EVENT_FUNCTION_EXIT = 1,
    EVENT_BREAKPOINT = 2
};

// 调试事件结构
struct debug_event {
    u64 timestamp;
    u32 pid;
    u32 tid;
    u32 cpu;
    u8 event_type;
    char function_name[MAX_FUNCTION_NAME];
    u64 instruction_pointer;
};

// 调试器控制状态
struct debugger_control {
    u8 debug_mode;
    u32 target_pid;
    u8 global_enable;
};

// Maps定义
struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(max_entries, 0);
    __uint(key_size, sizeof(u32));
    __uint(value_size, sizeof(u32));
} events SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, u32);
    __type(value, struct debugger_control);
} control_map SEC(".maps");

// 获取调试器控制状态
static inline struct debugger_control* get_debugger_control() {
    u32 key = 0;
    return bpf_map_lookup_elem(&control_map, &key);
}

// 检查是否应该跟踪当前进程
static inline int should_trace_process(struct debugger_control *ctrl) {
    if (!ctrl || !ctrl->global_enable) return 0;
    
    u32 current_pid = bpf_get_current_pid_tgid() >> 32;
    return (ctrl->target_pid == 0 || ctrl->target_pid == current_pid);
}

// 安全的字符串复制
static inline void safe_strcpy(char *dst, const char *src, int max_len) {
    int i;
    for (i = 0; i < max_len - 1 && src && src[i]; i++) {
        dst[i] = src[i];
    }
    dst[i] = '\0';
}

// 发送调试事件
static inline void send_debug_event(void *ctx, u8 event_type, const char *func_name) {
    struct debugger_control *ctrl = get_debugger_control();
    if (!should_trace_process(ctrl)) return;
    
    struct debug_event event = {};
    
    event.timestamp = bpf_ktime_get_ns();
    event.pid = bpf_get_current_pid_tgid() >> 32;
    event.tid = bpf_get_current_pid_tgid() & 0xFFFFFFFF;
    event.cpu = bpf_get_smp_processor_id();
    event.event_type = event_type;
    event.instruction_pointer = 0; // 简化版本，避免架构特定代码
    
    // 复制函数名
    if (func_name) {
        safe_strcpy(event.function_name, func_name, MAX_FUNCTION_NAME);
    }
    
    // 发送事件
    bpf_perf_event_output(ctx, &events, BPF_F_CURRENT_CPU, &event, sizeof(event));
}

// 通用kprobe处理函数
SEC("kprobe")
int generic_kprobe(struct pt_regs *ctx) {
    send_debug_event(ctx, EVENT_FUNCTION_ENTRY, "traced_function");
    return 0;
}

// 通用kretprobe处理函数
SEC("kretprobe")
int generic_kretprobe(struct pt_regs *ctx) {
    send_debug_event(ctx, EVENT_FUNCTION_EXIT, "traced_function");
    return 0;
}

// taco_sys函数跟踪
SEC("kprobe/taco_sys_mmz_alloc")
int trace_taco_sys_mmz_alloc_entry(struct pt_regs *ctx) {
    send_debug_event(ctx, EVENT_FUNCTION_ENTRY, "taco_sys_mmz_alloc");
    return 0;
}

SEC("kretprobe/taco_sys_mmz_alloc")
int trace_taco_sys_mmz_alloc_exit(struct pt_regs *ctx) {
    send_debug_event(ctx, EVENT_FUNCTION_EXIT, "taco_sys_mmz_alloc");
    return 0;
}

// 内核内存分配跟踪
SEC("kprobe/__kmalloc")
int trace_kmalloc_entry(struct pt_regs *ctx) {
    send_debug_event(ctx, EVENT_FUNCTION_ENTRY, "__kmalloc");
    return 0;
}

SEC("kretprobe/__kmalloc")
int trace_kmalloc_exit(struct pt_regs *ctx) {
    send_debug_event(ctx, EVENT_FUNCTION_EXIT, "__kmalloc");
    return 0;
}

char LICENSE[] SEC("license") = "GPL";
