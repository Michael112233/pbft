#!/bin/bash

# 精选日志收集脚本（并行，按角色精确收集）

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

echo -e "${BLUE}Starting selected log collection from all servers...${NC}"

# 定义服务器配置（端口:server_name）
SERVERS="26010:client|26011:node-0|26012:node-1|26013:node-2|26014:node-3"

# 创建并清理 parallel_logs 目录
echo -e "${YELLOW}Preparing parallel_logs directory...${NC}"
mkdir -p parallel_logs

# 按端口分类创建目录
for server_config in $(echo "$SERVERS" | tr '|' ' '); do
    port=$(echo "$server_config" | cut -d':' -f1)
    server_name=$(echo "$server_config" | cut -d':' -f2)
    mkdir -p "parallel_logs/port_${port}_${server_name}"
done

collect_selected_logs() {
    local port=$1
    local server_name=$2
    local log_dir="parallel_logs/port_${port}_${server_name}"

    echo -e "${YELLOW}[Port $port] Collecting logs from $server_name...${NC}"

    # 公共重要文件
    local files=(
        "pbft/logs/blockchain.log"
        "pbft/logs/others.log"
        "pbft/logs/result.log"
        "pbft/tps_results.csv"
        "pbft/latency_plot.png"
        "pbft/tps_plot.png"
    )

    # 角色特定文件
    if [ "$server_name" = "client" ]; then
        files+=("pbft/logs/client.log")
    else
        local node_id=$(echo "$server_name" | cut -d'-' -f2)
        if [[ -n "$node_id" ]]; then
            files+=("pbft/logs/node_${node_id}.log")
        fi
    fi

    # 收集文件（直接使用 expect + scp，避免任何交互式口令输入）
    for remote_path in "${files[@]}"; do
        local filename=$(basename "$remote_path")
        local local_path="$log_dir/$filename"

        # 使用 expect 处理密钥口令并执行 scp
        expect -c "
        set timeout 60
        spawn scp -i $KEY_PATH -P $port -o StrictHostKeyChecking=no $USERNAME@$HOST:~/$remote_path $local_path
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
        " > /dev/null 2>&1

        if [ $? -eq 0 ] && [ -f "$local_path" ] && [ -s "$local_path" ]; then
            echo -e "${GREEN}[Port $port] ✓ $filename ($(wc -c < "$local_path") bytes)${NC}"
        else
            echo -e "${RED}[Port $port] ✗ $filename${NC}"
        fi
    done

    # 写入服务器信息
    cat > "$log_dir/server_info.txt" << EOF
Server: $HOST
Port: $port
Server Name: $server_name
Collection Time: $(date)
Collection Type: Selected
EOF

    echo -e "${GREEN}[Port $port] Selected collection completed for $server_name${NC}"
}

echo -e "${BLUE}Starting parallel selected log collection...${NC}"
pids=()

for server_config in $(echo "$SERVERS" | tr '|' ' '); do
    port=$(echo "$server_config" | cut -d':' -f1)
    server_name=$(echo "$server_config" | cut -d':' -f2)
    collect_selected_logs "$port" "$server_name" &
    pids+=($!)
done

echo -e "${BLUE}Waiting for all selected log collection processes to complete...${NC}"
success_count=0
total_count=${#pids[@]}

for pid in "${pids[@]}"; do
    if wait "$pid"; then
        ((success_count++))
    fi
done

echo ""
echo "=========================================="
echo "SELECTED LOG COLLECTION SUMMARY"
echo "=========================================="

for server_config in $(echo "$SERVERS" | tr '|' ' '); do
    port=$(echo "$server_config" | cut -d':' -f1)
    server_name=$(echo "$server_config" | cut -d':' -f2)
    log_dir="parallel_logs/port_${port}_${server_name}"

    if [ -d "$log_dir" ] && [ "$(ls -A "$log_dir" 2>/dev/null)" ]; then
        echo -e "Port $port ($server_name): ${GREEN}SUCCESS${NC}"
    else
        echo -e "Port $port ($server_name): ${RED}FAILED${NC}"
    fi
done

echo ""
echo "Successful: $success_count/$total_count"
echo "Logs organized in: parallel_logs/"

if [ $success_count -eq $total_count ]; then
    echo -e "${GREEN}All selected logs collected successfully!${NC}"
    exit 0
else
    echo -e "${RED}Some servers failed to collect selected logs.${NC}"
    exit 1
fi


