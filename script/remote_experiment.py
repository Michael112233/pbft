#!/usr/bin/env python3
"""
Remote PBFT Experiment Script
Generates commands for running PBFT nodes and clients on remote machines
"""

import os
import sys
import argparse
import subprocess
import time
import json
try:
    import requests
    import urllib.parse
except ImportError:
    print("Error: 'requests' library not found. Please install it with: pip install requests")
    sys.exit(1)

class RemotePBFTExperiment:
    def __init__(self):
        self.base_ip = "192.168.1"
        self.client_ip = "192.168.1.255"
        self.project_path = "/path/to/pbft"  # 需要根据实际情况修改
        self.config_file = "config/run.json"
        # CSV file download URL
        self.csv_url = "https://drive.google.com/file/d/1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-/view"
        self.csv_filename = "len3_data.csv"
        self.data_dir = "data"
    
    def download_csv_file(self):
        """Download CSV file from Google Drive"""
        print("Downloading CSV file...")
        
        # Create data directory if it doesn't exist
        os.makedirs(self.data_dir, exist_ok=True)
        
        csv_path = os.path.join(self.data_dir, self.csv_filename)
        
        # Check if file already exists
        if os.path.exists(csv_path):
            print(f"CSV file already exists: {csv_path}")
            return True
        
        try:
            # Convert Google Drive share URL to direct download URL
            file_id = "1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-"
            
            # First, try to get the download confirmation URL
            session = requests.Session()
            direct_url = f"https://drive.google.com/uc?export=download&id={file_id}"
            
            print(f"Downloading from: {direct_url}")
            
            # First request to get the confirmation page
            response = session.get(direct_url, stream=True, timeout=30)
            response.raise_for_status()
            
            # Check if we got the virus scan warning page
            if "Google Drive can't scan this file for viruses" in response.text:
                print("File requires virus scan confirmation. Getting download link...")
                
                # Extract the download form action URL
                import re
                form_match = re.search(r'action="([^"]*)"', response.text)
                if form_match:
                    download_url = form_match.group(1)
                    # Add the form parameters
                    download_url += f"?id={file_id}&export=download&confirm=t"
                    
                    print(f"Downloading from confirmation URL: {download_url}")
                    response = session.get(download_url, stream=True, timeout=60)
                    response.raise_for_status()
                else:
                    raise Exception("Could not find download confirmation URL")
            
            # Save the file
            with open(csv_path, 'wb') as f:
                for chunk in response.iter_content(chunk_size=8192):
                    f.write(chunk)
            
            print(f"CSV file downloaded successfully: {csv_path}")
            return True
            
        except requests.exceptions.RequestException as e:
            print(f"Error downloading CSV file: {e}")
            print("Please manually download the file from:")
            print(self.csv_url)
            print(f"And place it in: {csv_path}")
            return False
        except Exception as e:
            print(f"Unexpected error downloading CSV file: {e}")
            return False
        
    def get_node_ip(self, node_id):
        """根据节点ID生成IP地址"""
        return f"{self.base_ip}.{node_id}"
    
    def generate_ssh_command(self, ip, role, node_id=None):
        """生成SSH连接和执行的命令"""
        if role == "client":
            command = f"./pbft_main -r client -m remote"
            target_ip = self.client_ip
        elif role == "node":
            if node_id is None:
                raise ValueError("node_id is required for node role")
            command = f"./pbft_main -r node -m remote -n {node_id}"
            target_ip = self.get_node_ip(node_id)
        else:
            raise ValueError("role must be 'client' or 'node'")
        
        # 完整的SSH命令
        ssh_command = f"ssh {target_ip} 'cd {self.project_path} && {command}'"
        return ssh_command, target_ip, command
    
    def generate_local_command(self, role, node_id=None):
        """生成本地执行的命令（用于复制到远程机器）"""
        if role == "client":
            return f"./pbft_main -r client -m remote"
        elif role == "node":
            if node_id is None:
                raise ValueError("node_id is required for node role")
            return f"./pbft_main -r node -m remote -n {node_id}"
        else:
            raise ValueError("role must be 'client' or 'node'")
    
    def generate_setup_commands(self, ip):
        """生成远程机器设置命令"""
        setup_commands = [
            f"# Setup commands for {ip}",
            f"ssh {ip} 'mkdir -p {self.project_path}'",
            f"# Copy project files to remote machine",
            f"rsync -avz --exclude='logs/' --exclude='*.log' ./ {ip}:{self.project_path}/",
            f"# Build project on remote machine",
            f"ssh {ip} 'cd {self.project_path} && go mod tidy && go build -o pbft_main main.go'",
            f"# Download CSV file on remote machine",
            f"ssh {ip} 'cd {self.project_path} && python3 -c \"import requests; import os; import sys; import re; os.makedirs(\\\"data\\\", exist_ok=True); file_id=\\\"1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-\\\"; session=requests.Session(); direct_url=f\\\"https://drive.google.com/uc?export=download&id={{file_id}}\\\"; response=session.get(direct_url, timeout=30); response.raise_for_status(); print(\\\"Checking for virus scan warning...\\\"); print(response.text[:200]); print(\\\"\\\"); if \\\"Google Drive can\\'t scan this file for viruses\\\" in response.text: print(\\\"File requires virus scan confirmation. Getting download link...\\\"); form_match=re.search(r\\\"action=\\\\\\\"([^\\\\\\\"]*)\\\\\\\"\\\", response.text); download_url=form_match.group(1) if form_match else None; download_url+=f\\\"?id={{file_id}}&export=download&confirm=t\\\" if download_url else None; response=session.get(download_url, timeout=60) if download_url else response; response.raise_for_status(); open(\\\"data/len3_data.csv\\\", \\\"wb\\\").write(response.content); print(\\\"CSV downloaded successfully\\\")\" || (echo \\\"CSV download failed\\\" && exit 1)'",
            f"# Create logs directory",
            f"ssh {ip} 'mkdir -p {self.project_path}/logs'",
            ""
        ]
        return setup_commands
    
    def generate_execution_script(self, role, node_id=None, total_nodes=4):
        """生成完整的执行脚本"""
        if role == "client":
            target_ip = self.client_ip
            command = self.generate_local_command(role)
        elif role == "node":
            if node_id is None:
                raise ValueError("node_id is required for node role")
            target_ip = self.get_node_ip(node_id)
            command = self.generate_local_command(role, node_id)
        else:
            raise ValueError("role must be 'client' or 'node'")
        
        script_content = f"""#!/bin/bash
# PBFT Remote Experiment Script
# Role: {role}
# Target IP: {target_ip}
# Node ID: {node_id if node_id is not None else 'N/A'}

echo "Starting PBFT {role} on {target_ip}..."
echo "Command: {command}"

# Change to project directory
cd {self.project_path}

# Check if binary exists
if [ ! -f "./pbft_main" ]; then
    echo "Error: pbft_main not found. Please build the project first."
    echo "Run: go mod tidy && go build -o pbft_main main.go"
    exit 1
fi

# Download CSV file if it doesn't exist
if [ ! -f "data/len3_data.csv" ]; then
    echo "Downloading CSV file..."
    python3 -c "
import requests
import os
import sys
import re
os.makedirs('data', exist_ok=True)
try:
    file_id = '1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-'
    session = requests.Session()
    direct_url = f'https://drive.google.com/uc?export=download&id={{file_id}}'
    
    # First request to get the confirmation page
    response = session.get(direct_url, timeout=30)
    response.raise_for_status()
    
    # Check if we got the virus scan warning page
    if 'Google Drive can\\'t scan this file for viruses' in response.text:
        print('File requires virus scan confirmation. Getting download link...')
        form_match = re.search(r'action=\"([^\"]*)\"', response.text)
        if form_match:
            download_url = form_match.group(1)
            download_url += f'?id={{file_id}}&export=download&confirm=t'
            response = session.get(download_url, timeout=60)
            response.raise_for_status()
        else:
            raise Exception('Could not find download confirmation URL')
    
    with open('data/len3_data.csv', 'wb') as f:
        f.write(response.content)
    print('CSV file downloaded successfully')
except Exception as e:
    print(f'Error downloading CSV file: {{e}}')
    print('Please manually download the file from: https://drive.google.com/file/d/1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-/view')
    print('And place it in: data/len3_data.csv')
    sys.exit(1)
"
    if [ $? -ne 0 ]; then
        echo "Error: CSV file download failed. Cannot continue without data file."
        exit 1
    fi
else
    echo "CSV file already exists"
fi

# Check if config file exists
if [ ! -f "{self.config_file}" ]; then
    echo "Error: Config file {self.config_file} not found."
    exit 1
fi

# Create logs directory if it doesn't exist
mkdir -p logs

# Start the application
echo "Executing: {command}"
{command}
"""
        return script_content
    
    def save_execution_script(self, role, node_id=None, total_nodes=4):
        """保存执行脚本到文件"""
        script_content = self.generate_execution_script(role, node_id, total_nodes)
        
        if role == "client":
            filename = f"run_client_remote.sh"
        else:
            filename = f"run_node_{node_id}_remote.sh"
        
        with open(filename, 'w') as f:
            f.write(script_content)
        
        # 使脚本可执行
        os.chmod(filename, 0o755)
        print(f"Generated execution script: {filename}")
        return filename
    
    def generate_deployment_guide(self, total_nodes=4):
        """生成部署指南"""
        guide_content = f"""# PBFT Remote Experiment Deployment Guide

## Prerequisites
1. All machines should have Go installed
2. SSH access should be configured between machines
3. Project files should be available on all machines

## Machine Configuration
- Client IP: {self.client_ip}
- Node IPs: {', '.join([self.get_node_ip(i) for i in range(total_nodes)])}

## Deployment Steps

### 1. Setup All Machines
"""
        
        # 为每台机器生成设置命令
        for i in range(total_nodes):
            node_ip = self.get_node_ip(i)
            guide_content += f"""
# Setup Node {i} ({node_ip})
ssh {node_ip} 'mkdir -p {self.project_path}'
rsync -avz --exclude='logs/' --exclude='*.log' ./ {node_ip}:{self.project_path}/
ssh {node_ip} 'cd {self.project_path} && go mod tidy && go build -o pbft_main main.go'
ssh {node_ip} 'cd {self.project_path} && python3 -c "import requests; import os; import sys; import re; os.makedirs(\\"data\\", exist_ok=True); file_id=\\"1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-\\"; session=requests.Session(); direct_url=f\\"https://drive.google.com/uc?export=download&id={{file_id}}\\"; response=session.get(direct_url, timeout=30); response.raise_for_status(); if \\"Google Drive can\\'t scan this file for viruses\\" in response.text: form_match=re.search(r\\"action=\\\\\\"([^\\\\\\"]*)\\\\\\"\\", response.text); download_url=form_match.group(1) if form_match else None; download_url+=f\\"?id={{file_id}}&export=download&confirm=t\\" if download_url else None; response=session.get(download_url, timeout=60) if download_url else response; response.raise_for_status(); open(\\"data/len3_data.csv\\", \\"wb\\").write(response.content); print(\\"CSV downloaded successfully\\")" || (echo "CSV download failed" && exit 1)'
ssh {node_ip} 'mkdir -p {self.project_path}/logs'
"""
        
        # 客户端设置
        guide_content += f"""
# Setup Client ({self.client_ip})
ssh {self.client_ip} 'mkdir -p {self.project_path}'
rsync -avz --exclude='logs/' --exclude='*.log' ./ {self.client_ip}:{self.project_path}/
ssh {self.client_ip} 'cd {self.project_path} && go mod tidy && go build -o pbft_main main.go'
ssh {self.client_ip} 'cd {self.project_path} && python3 -c "import requests; import os; import sys; import re; os.makedirs(\\"data\\", exist_ok=True); file_id=\\"1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-\\"; session=requests.Session(); direct_url=f\\"https://drive.google.com/uc?export=download&id={{file_id}}\\"; response=session.get(direct_url, timeout=30); response.raise_for_status(); if \\"Google Drive can\\'t scan this file for viruses\\" in response.text: form_match=re.search(r\\"action=\\\\\\"([^\\\\\\"]*)\\\\\\"\\", response.text); download_url=form_match.group(1) if form_match else None; download_url+=f\\"?id={{file_id}}&export=download&confirm=t\\" if download_url else None; response=session.get(download_url, timeout=60) if download_url else response; response.raise_for_status(); open(\\"data/len3_data.csv\\", \\"wb\\").write(response.content); print(\\"CSV downloaded successfully\\")" || (echo "CSV download failed" && exit 1)'
ssh {self.client_ip} 'mkdir -p {self.project_path}/logs'

### 2. Start All Nodes
"""
        
        # 启动所有节点
        for i in range(total_nodes):
            node_ip = self.get_node_ip(i)
            command = self.generate_local_command("node", i)
            guide_content += f"""
# Start Node {i} on {node_ip}
ssh {node_ip} 'cd {self.project_path} && {command}' &
"""
        
        # 启动客户端
        guide_content += f"""
### 3. Start Client
# Start Client on {self.client_ip}
ssh {self.client_ip} 'cd {self.project_path} && {self.generate_local_command("client")}' &

### 4. Monitor Logs
"""
        
        # 监控日志的命令
        for i in range(total_nodes):
            node_ip = self.get_node_ip(i)
            guide_content += f"""
# Monitor Node {i} logs
ssh {node_ip} 'tail -f {self.project_path}/logs/node_{i}.log'
"""
        
        guide_content += f"""
# Monitor Client logs
ssh {self.client_ip} 'tail -f {self.project_path}/logs/client.log'

### 5. Stop All Processes
"""
        
        # 停止所有进程的命令
        for i in range(total_nodes):
            node_ip = self.get_node_ip(i)
            guide_content += f"""
# Stop Node {i}
ssh {node_ip} 'pkill -f pbft_main'
"""
        
        guide_content += f"""
# Stop Client
ssh {self.client_ip} 'pkill -f pbft_main'

## Notes
- Make sure all machines can communicate with each other
- Check firewall settings if connections fail
- Monitor system resources during experiment
- Logs are stored in logs/ directory on each machine
"""
        
        return guide_content
    
    def save_deployment_guide(self, total_nodes=4):
        """保存部署指南到文件"""
        guide_content = self.generate_deployment_guide(total_nodes)
        filename = "remote_deployment_guide.md"
        
        with open(filename, 'w') as f:
            f.write(guide_content)
        
        print(f"Generated deployment guide: {filename}")
        return filename

def main():
    parser = argparse.ArgumentParser(description='Generate remote PBFT experiment commands')
    parser.add_argument('--role', '-r', choices=['node', 'client'], required=True,
                       help='Role: node or client')
    parser.add_argument('--node-id', '-n', type=int,
                       help='Node ID (required for node role)')
    parser.add_argument('--total-nodes', '-t', type=int, default=4,
                       help='Total number of nodes (default: 4)')
    parser.add_argument('--generate-all', '-a', action='store_true',
                       help='Generate all scripts and deployment guide')
    parser.add_argument('--project-path', '-p', default='/path/to/pbft',
                       help='Project path on remote machines (default: /path/to/pbft)')
    
    args = parser.parse_args()
    
    # 验证参数
    if args.role == 'node' and args.node_id is None:
        print("Error: --node-id is required for node role")
        sys.exit(1)
    
    if args.node_id is not None and (args.node_id < 0 or args.node_id >= args.total_nodes):
        print(f"Error: node-id must be between 0 and {args.total_nodes-1}")
        sys.exit(1)
    
    # 创建实验对象
    experiment = RemotePBFTExperiment()
    experiment.project_path = args.project_path
    
    if args.generate_all:
        print("Generating all remote experiment files...")
        
        # 生成所有节点的执行脚本
        for i in range(args.total_nodes):
            experiment.save_execution_script('node', i, args.total_nodes)
        
        # 生成客户端执行脚本
        experiment.save_execution_script('client', None, args.total_nodes)
        
        # 生成部署指南
        experiment.save_deployment_guide(args.total_nodes)
        
        print(f"\nGenerated files:")
        print(f"- run_node_*_remote.sh (for each node)")
        print(f"- run_client_remote.sh (for client)")
        print(f"- remote_deployment_guide.md (deployment instructions)")
        
    else:
        # 生成单个角色的执行脚本
        script_file = experiment.save_execution_script(args.role, args.node_id, args.total_nodes)
        
        # 显示SSH命令
        ssh_cmd, target_ip, local_cmd = experiment.generate_ssh_command(
            target_ip=None, role=args.role, node_id=args.node_id
        )
        
        print(f"\nGenerated execution script: {script_file}")
        print(f"Target IP: {target_ip}")
        print(f"Local command: {local_cmd}")
        print(f"SSH command: {ssh_cmd}")
        
        # 显示设置命令
        print(f"\nSetup commands for {target_ip}:")
        setup_commands = experiment.generate_setup_commands(target_ip)
        for cmd in setup_commands:
            print(cmd)

if __name__ == "__main__":
    main()
