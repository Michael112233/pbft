#!/usr/bin/env python3
"""
Linux Remote Experiment Script
Runs specific node or client on remote server based on nodeID and role
"""

import subprocess
import argparse
import sys
import os
import time
import signal


class LinuxRemoteExperiment:
    def __init__(self, node_id: int, role: str):
        self.node_id = node_id
        self.role = role
        self.process = None
        
    def cleanup_log_files(self):
        """Delete all log files before starting the experiment"""
        try:
            print("Cleaning log files...")
            logs_dir = os.path.join(os.getcwd(), "logs")
            
            if not os.path.exists(logs_dir):
                print("Logs directory does not exist")
                return True
            
            # List of log files to clean
            log_files = [
                "blockchain.log",
                "client.log", 
                "node_0.log",
                "node_1.log",
                "node_2.log",
                "node_3.log",
                "others.log",
                "result.log"
            ]
            
            cleaned_count = 0
            for log_file in log_files:
                log_path = os.path.join(logs_dir, log_file)
                if os.path.exists(log_path):
                    os.remove(log_path)
                    cleaned_count += 1
                    print(f"Deleted {log_file}")
            
            print(f"Cleaned {cleaned_count} log files")
            return True
            
        except Exception as e:
            print(f"Error cleaning log files: {e}")
            return False
    
    def clean_ports(self):
        """Clean ports that might be in use"""
        required_ports = [20000, 28000, 28100, 28200, 28300]
        
        for port in required_ports:
            try:
                # Use lsof to find the processes occupying the ports
                result = subprocess.run([
                    "lsof", "-ti", f":{port}"
                ], capture_output=True, text=True)
                
                if result.returncode == 0 and result.stdout.strip():
                    pids = result.stdout.strip().split('\n')
                    for pid in pids:
                        subprocess.run(["kill", "-9", pid], check=True)
                        print(f"Killed process {pid} on port {port}")
            except:
                pass
    
    def check_go_installed(self):
        """Check if Go is installed"""
        try:
            result = subprocess.run([
                "go", "version"
            ], capture_output=True, text=True, timeout=10)
            
            if result.returncode != 0:
                print(f"Go is not installed or not working properly: {result.stderr}")
                return False
            
            print(f"Go is already installed: {result.stdout.strip()}")
            return True

        except subprocess.TimeoutExpired:
            print(f"Go version check timed out")
            return False
        except Exception as e:
            print(f"Go version check failed with error: {e}")
            return False
    
    def build_go_project(self):
        """Build the Go project"""
        try:
            # 1. run "go mod tidy"
            print("Running go mod tidy...")
            result = subprocess.run([
                "go", "mod", "tidy"
            ], capture_output=True, text=True, timeout=60)
            
            if result.returncode != 0:
                print(f"go mod tidy failed: {result.stderr}")
                return False
            
            # 2. build the executable file
            print("Building Go project...")
            result = subprocess.run([
                "go", "build", "-o", "pbft_main", "main.go"
            ], capture_output=True, text=True, timeout=120)
            
            if result.returncode != 0:
                print(f"Build failed: {result.stderr}")
                return False
            
            print(f"Build completed successfully")
            return True

        except subprocess.TimeoutExpired:
            print(f"Build timed out")
            return False
        except Exception as e:
            print(f"Build failed with error: {e}")
            return False
    
    def start_node(self):
        """Start a specific node"""
        try:
            print(f"Starting node {self.node_id}...")
            
            # Start the node process
            self.process = subprocess.Popen([
                "./pbft_main", "-r", "node", "-m", "remote", "-n", str(self.node_id)
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            
            print(f"Node {self.node_id} started with PID {self.process.pid}")
            return True
            
        except Exception as e:
            print(f"Failed to start node {self.node_id}: {e}")
            return False
    
    def start_client(self):
        """Start the client"""
        try:
            print("Starting client...")
            
            # Start the client process
            self.process = subprocess.Popen([
                "./pbft_main", "-r", "client", "-m", "remote"
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            
            print(f"Client started with PID {self.process.pid}")
            return True
            
        except Exception as e:
            print(f"Failed to start client: {e}")
            return False
    
    def signal_handler(self, signum, frame):
        """Handle interrupt signals"""
        print(f"\nReceived signal {signum}, shutting down...")
        if self.process:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
        sys.exit(0)
    
    def run_experiment(self):
        """Run the remote experiment"""
        print(f"Starting Linux Remote PBFT Experiment - {self.role} {self.node_id}")
        
        try:
            # Set up signal handlers
            signal.signal(signal.SIGINT, self.signal_handler)
            signal.signal(signal.SIGTERM, self.signal_handler)
            
            # Step 1: Cleanup
            print("Performing pre-experiment cleanup...")
            self.clean_log_files()
            self.clean_ports()
            
            # Step 2: Check Go installation
            if not self.check_go_installed():
                print("Go installation check failed")
                return False
            
            # Step 3: Build project
            if not self.build_go_project():
                print("Project build failed")
                return False
            
            # Step 4: Start the appropriate process
            if self.role == "node":
                if not self.start_node():
                    print("Failed to start node")
                    return False
            elif self.role == "client":
                if not self.start_client():
                    print("Failed to start client")
                    return False
            else:
                print(f"Unknown role: {self.role}")
                return False
            
            print(f"{self.role} {self.node_id} started successfully!")
            print("Press Ctrl+C to stop the experiment")
            
            # Wait for the process to complete or be interrupted
            try:
                self.process.wait()
            except KeyboardInterrupt:
                print("\nExperiment interrupted by user")
                self.signal_handler(signal.SIGINT, None)
            
            return True
            
        except Exception as e:
            print(f"Experiment error: {e}")
            return False


def main():
    parser = argparse.ArgumentParser(description='Linux Remote PBFT Experiment')
    parser.add_argument('node_id', type=int, help='Node ID (0-3 for nodes, 0 for client)')
    parser.add_argument('role', choices=['node', 'client'], help='Role: node or client')
    
    args = parser.parse_args()
    
    # Validate arguments
    if args.role == 'node' and (args.node_id < 0 or args.node_id > 3):
        print("Error: Node ID must be between 0 and 3 for nodes")
        sys.exit(1)
    
    if args.role == 'client' and args.node_id != 0:
        print("Warning: Client node_id should be 0, but continuing anyway")
    
    # Create and run experiment
    experiment = LinuxRemoteExperiment(args.node_id, args.role)
    success = experiment.run_experiment()
    
    if success:
        print("Experiment completed successfully")
        sys.exit(0)
    else:
        print("Experiment failed")
        sys.exit(1)


if __name__ == "__main__":
    main()
