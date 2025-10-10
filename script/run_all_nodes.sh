#!/bin/bash

# PBFT并行节点启动脚本
# 同时在所有CloudLab服务器上启动PBFT节点

HOST="c220g2-010811.wisc.cloudlab.us"
USERNAME="wucy"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Starting PBFT nodes on all servers...${NC}"

# 定义服务器配置
declare -A SERVERS=(
    ["25610"]="client"
    ["25611"]="node --node-id 0"
    ["25612"]="node --node-id 1" 
    ["25613"]="node --node-id 2"
    ["25614"]="node --node-id 3"
)

# 创建日志目录
LOG_DIR="./parallel_logs"
mkdir -p "$LOG_DIR"

# 并行执行函数
run_server() {
    local port=$1
    local role=$2
    local log_file="$LOG_DIR/server_${port}.log"
    
    echo -e "${YELLOW}[Port $port] Starting $role...${NC}"
    
    # 执行SSH命令
    ssh -p "$port" "$USERNAME@$HOST" \
        -o ConnectTimeout=10 \
        -o StrictHostKeyChecking=no \
        "cd pbft && ./remote_run_linux.sh --role $role" \
        > "$log_file" 2>&1
    
    local exit_code=$?
    
    if [ $exit_code -eq 0 ]; then
        echo -e "${GREEN}[Port $port] SUCCESS: $role completed${NC}"
    else
        echo -e "${RED}[Port $port] FAILED: $role (exit code: $exit_code)${NC}"
        echo -e "${RED}[Port $port] Check log: $log_file${NC}"
    fi
    
    return $exit_code
}

# 启动所有节点和客户端（客户端延迟5秒）
echo -e "${BLUE}Starting all PBFT nodes...${NC}"
node_pids=()
client_pids=()
client_ports=()

# 立即启动所有节点
for port in "${!SERVERS[@]}"; do
    role="${SERVERS[$port]}"
    if [[ "$role" == "client" ]]; then
        client_ports+=("$port")
    else
        echo -e "${YELLOW}[Port $port] Starting $role immediately...${NC}"
        run_server "$port" "$role" &
        node_pids+=($!)
    fi
done

# 延迟5秒后启动客户端
if [ ${#client_ports[@]} -gt 0 ]; then
    (
        sleep 5
        echo -e "${BLUE}5 seconds elapsed, starting client...${NC}"
        for port in "${client_ports[@]}"; do
            role="${SERVERS[$port]}"
            echo -e "${YELLOW}[Port $port] Starting $role...${NC}"
            run_server "$port" "$role"
        done
    ) &
    client_pids+=($!)
fi

echo -e "${BLUE}All processes started. Waiting for completion...${NC}"

# 等待所有进程完成
all_pids=("${node_pids[@]}" "${client_pids[@]}")
success_count=0
total_count=${#all_pids[@]}

for pid in "${all_pids[@]}"; do
    if wait "$pid"; then
        ((success_count++))
    fi
done

# 输出结果
echo ""
echo "=========================================="
echo "EXECUTION SUMMARY"
echo "=========================================="

for port in "${!SERVERS[@]}"; do
    role="${SERVERS[$port]}"
    log_file="$LOG_DIR/server_${port}.log"
    
    if [ -f "$log_file" ] && [ -s "$log_file" ]; then
        # 检查日志文件最后几行来判断成功状态
        if tail -5 "$log_file" | grep -q -i "error\|failed\|exception"; then
            echo -e "Port $port ($role): ${RED}FAILED${NC}"
        else
            echo -e "Port $port ($role): ${GREEN}SUCCESS${NC}"
        fi
    else
        echo -e "Port $port ($role): ${RED}NO OUTPUT${NC}"
    fi
done

echo ""
echo "Successful: $success_count/$total_count"
echo "Logs available in: $LOG_DIR/"

if [ $success_count -eq $total_count ]; then
    echo -e "${GREEN}All servers completed successfully!${NC}"
    exit 0
else
    echo -e "${RED}Some servers failed. Check logs for details.${NC}"
    exit 1
fi
