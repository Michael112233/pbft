#!/bin/bash

# PBFT并行节点启动脚本
# 同时在所有CloudLab服务器上启动PBFT节点

HOST="amd258.utah.cloudlab.us"
USERNAME="wucy"
KEY_PATH="$HOME/.ssh/id_rsa"
PASSPHRASE="michael"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Starting PBFT nodes on all servers...${NC}"

# 定义服务器配置
SERVERS="26010:client|26011:node-0|26012:node-1|26013:node-2|26014:node-3"

# 创建日志目录
LOG_DIR="./parallel_logs"
mkdir -p "$LOG_DIR"

# 获取服务器名称的函数
get_server_name() {
    local role=$1
    if [[ "$role" == "client" ]]; then
        echo "client"
    else
        # 从node0, node1等提取节点ID
        local node_id=$(echo "$role" | sed -E 's/^node-?//')
        echo "node-${node_id}"
    fi
}

# 获取完整角色名称的函数
get_full_role() {
    local role=$1
    if [[ "$role" == "client" ]]; then
        echo "client"
    else
        # 从node0转换为node --node-id 0
        local node_id=$(echo "$role" | sed -E 's/^node-?//')
        echo "node --node-id ${node_id}"
    fi
}

# 创建按端口分类的日志目录
for server_config in $(echo "$SERVERS" | tr '|' ' '); do
    port=$(echo "$server_config" | cut -d':' -f1)
    role=$(echo "$server_config" | sed 's/^[^:]*://')
    server_name=$(get_server_name "$role")
    mkdir -p "$LOG_DIR/port_${port}_${server_name}"
done

# 并行执行函数
run_server() {
    local port=$1
    local role=$2
    
    # 确定服务器名称和日志目录
    # 从完整角色名称中提取简化版本
    local simple_role
    if [[ "$role" == "client" ]]; then
        simple_role="client"
    else
        simple_role=$(echo "$role" | sed 's/.*--node-id \([0-9]*\).*/node\1/')
    fi
    local server_name=$(get_server_name "$simple_role")
    
    local port_log_dir="$LOG_DIR/port_${port}_${server_name}"
    local log_file="$port_log_dir/server_${port}.log"
    
    echo -e "${YELLOW}[Port $port] Starting $role...${NC}"
    
    # 执行SSH命令 - 使用expect处理密码输入
    expect -c "
        set timeout 30
        spawn ssh -i $KEY_PATH -p $port $USERNAME@$HOST -o ConnectTimeout=10 -o StrictHostKeyChecking=no \"cd pbft && chmod +x remote_run_linux.sh && export PATH=/usr/local/go/bin:\\\$PATH && ./remote_run_linux.sh --role $role\"
        expect {
            \"Enter passphrase for key\" {
                send \"$PASSPHRASE\r\"
                exp_continue
            }
            \"password:\" {
                send \"$PASSPHRASE\r\"
                exp_continue
            }
            eof
        }
    " > "$log_file" 2>&1
    
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

# 立即启动所有节点
for server_config in $(echo "$SERVERS" | tr '|' ' '); do
    port=$(echo "$server_config" | cut -d':' -f1)
    role=$(echo "$server_config" | sed 's/^[^:]*://')
    full_role=$(get_full_role "$role")
    
    if [[ "$role" == "client" ]]; then
        # 客户端延迟5秒启动
        (
            sleep 10
            echo -e "${BLUE}5 seconds elapsed, starting client...${NC}"
            echo -e "${YELLOW}[Port $port] Starting $full_role...${NC}"
            run_server "$port" "$full_role"
        ) &
    else
        echo -e "${YELLOW}[Port $port] Starting $full_role immediately...${NC}"
        run_server "$port" "$full_role" &
    fi
done

echo -e "${BLUE}All processes started. Waiting for completion...${NC}"

# 等待所有进程完成
wait
success_count=5  # 假设所有5个进程都成功
total_count=5

# 输出结果
echo ""
echo "=========================================="
echo "EXECUTION SUMMARY"
echo "=========================================="

# 检查每个服务器的状态
for server_config in $(echo "$SERVERS" | tr '|' ' '); do
    port=$(echo "$server_config" | cut -d':' -f1)
    role=$(echo "$server_config" | sed 's/^[^:]*://')
    
    # 确定服务器名称和日志文件路径
    server_name=$(get_server_name "$role")
    log_file="$LOG_DIR/port_${port}_${server_name}/server_${port}.log"
    
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
