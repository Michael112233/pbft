#!/usr/bin/env python3
"""
Local PBFT Experiment Script
Automatically runs PBFT nodes and clients based on run.json configuration
"""

import json
import os
import subprocess
import time
import signal
import sys
from pathlib import Path

class PBFTExperiment:
    def __init__(self, config_path="config/run.json"):
        self.config_path = config_path
        self.config = self.load_config()
        self.processes = []
        
    def load_config(self):
        """Load configuration from run.json"""
        try:
            with open(self.config_path, 'r') as f:
                return json.load(f)
        except FileNotFoundError:
            print(f"Error: {self.config_path} not found")
            sys.exit(1)
        except json.JSONDecodeError:
            print(f"Error: Invalid JSON in {self.config_path}")
            sys.exit(1)
    
    def build_project(self):
        """Build the PBFT project using run_project.sh"""
        print("Building PBFT project...")
        try:
            result = subprocess.run(["./run_project.sh"], 
                                  capture_output=True, text=True, timeout=60)
            if result.returncode != 0:
                print(f"Build failed: {result.stderr}")
                return False
            print("Build successful!")
            return True
        except subprocess.TimeoutExpired:
            print("Build timeout")
            return False
        except Exception as e:
            print(f"Build error: {e}")
            return False
    
    def start_node(self, node_id):
        """Start a PBFT node with given node_id"""
        print(f"Starting node {node_id}...")
        try:
            # Create a temporary config file for this node
            temp_config = self.config.copy()
            temp_config["node_id"] = node_id
            temp_config_path = f"config/temp_node_{node_id}.json"
            
            with open(temp_config_path, 'w') as f:
                json.dump(temp_config, f, indent=4)
            
            # Start the node process
            cmd = ["./pbft_main", "node", "local", temp_config_path]
            process = subprocess.Popen(cmd, 
                                     stdout=subprocess.PIPE, 
                                     stderr=subprocess.PIPE,
                                     text=True)
            self.processes.append(process)
            print(f"Node {node_id} started with PID {process.pid}")
            return True
        except Exception as e:
            print(f"Failed to start node {node_id}: {e}")
            return False
    
    def start_client(self, client_id):
        """Start a PBFT client with given client_id"""
        print(f"Starting client {client_id}...")
        try:
            # Create a temporary config file for this client
            temp_config = self.config.copy()
            temp_config["node_id"] = client_id
            temp_config_path = f"config/temp_client_{client_id}.json"
            
            with open(temp_config_path, 'w') as f:
                json.dump(temp_config, f, indent=4)
            
            # Start the client process
            cmd = ["./pbft_main", "client", "local", temp_config_path]
            process = subprocess.Popen(cmd, 
                                     stdout=subprocess.PIPE, 
                                     stderr=subprocess.PIPE,
                                     text=True)
            self.processes.append(process)
            print(f"Client {client_id} started with PID {process.pid}")
            return True
        except Exception as e:
            print(f"Failed to start client {client_id}: {e}")
            return False
    
    def cleanup_temp_configs(self):
        """Remove temporary configuration files"""
        print("Cleaning up temporary config files...")
        for i in range(self.config["node_num"]):
            temp_file = f"config/temp_node_{i}.json"
            if os.path.exists(temp_file):
                os.remove(temp_file)
        
        # Clean up client configs (assuming 1 client for now)
        temp_file = "config/temp_client_0.json"
        if os.path.exists(temp_file):
            os.remove(temp_file)
    
    def signal_handler(self, signum, frame):
        """Handle interrupt signals"""
        print("\nReceived interrupt signal. Shutting down...")
        self.shutdown()
        sys.exit(0)
    
    def shutdown(self):
        """Shutdown all processes"""
        print("Shutting down all processes...")
        for process in self.processes:
            if process.poll() is None:  # Process is still running
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
        
        self.cleanup_temp_configs()
        print("All processes terminated.")
    
    def run_experiment(self):
        """Run the complete experiment"""
        print("Starting PBFT Local Experiment")
        print(f"Configuration: {self.config}")
        
        # Set up signal handlers
        signal.signal(signal.SIGINT, self.signal_handler)
        signal.signal(signal.SIGTERM, self.signal_handler)
        
        try:
            # Build the project
            if not self.build_project():
                return False
            
            # Start all nodes
            for node_id in range(self.config["node_num"]):
                if not self.start_node(node_id):
                    return False
                time.sleep(1)  # Small delay between node starts
            
            # Start client
            if not self.start_client(0):
                return False
            
            print(f"\nExperiment started successfully!")
            print(f"Running {self.config['node_num']} nodes and 1 client")
            print("Press Ctrl+C to stop the experiment")
            
            # Wait for processes to complete or be interrupted
            while True:
                time.sleep(1)
                # Check if any process has died
                for i, process in enumerate(self.processes):
                    if process.poll() is not None:
                        print(f"Process {i} (PID {process.pid}) has terminated")
                        stdout, stderr = process.communicate()
                        if stdout:
                            print(f"STDOUT: {stdout}")
                        if stderr:
                            print(f"STDERR: {stderr}")
                
        except KeyboardInterrupt:
            print("\nExperiment interrupted by user")
        except Exception as e:
            print(f"Experiment error: {e}")
        finally:
            self.shutdown()
        
        return True

def main():
    """Main function"""
    if len(sys.argv) > 1:
        config_path = sys.argv[1]
    else:
        config_path = "config/run.json"
    
    experiment = PBFTExperiment(config_path)
    success = experiment.run_experiment()
    
    if success:
        print("Experiment completed successfully")
    else:
        print("Experiment failed")
        sys.exit(1)

if __name__ == "__main__":
    main()
