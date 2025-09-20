#!/usr/bin/env python3
"""
Local PBFT Experiment Script
Runs client and node in separate terminals
"""

import os
import subprocess
import time
import signal
import sys
import shutil

class PBFTExperiment:
    def __init__(self):
        self.processes = []
        
    def clean_logs(self):
        """Delete existing logs"""
        print("Cleaning existing logs...")
        if os.path.exists("logs"):
            shutil.rmtree("logs")
            print("Logs directory deleted")
        else:
            print("No logs directory found")
    
    def build_project(self):
        """Build the PBFT project directly"""
        print("Building PBFT project...")
        try:
            # Clean logs folder
            if os.path.exists("logs"):
                shutil.rmtree("logs")
            
            # Build project directly
            result = subprocess.run(["go", "mod", "tidy"], 
                                  capture_output=True, text=True, timeout=30)
            if result.returncode != 0:
                print(f"go mod tidy failed: {result.stderr}")
                return False
            
            result = subprocess.run(["go", "build", "-o", "pbft_main", "main.go"], 
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
    
    def start_client_terminal(self):
        """Start client in a new terminal"""
        print("Starting client in new terminal...")
        try:
            # Use Terminal.app for macOS
            cmd = ["osascript", "-e", 
                   f'tell application "Terminal" to do script "cd {os.getcwd()} && ./pbft_main -r client -m local"']
            process = subprocess.Popen(cmd)
            self.processes.append(process)
            print("Client terminal started")
            return True
        except FileNotFoundError:
            print("Terminal.app not found")
            return False
    
    def start_node_terminal(self):
        """Start node in a new terminal"""
        print("Starting node in new terminal...")
        try:
            # Use Terminal.app for macOS
            cmd = ["osascript", "-e", 
                   f'tell application "Terminal" to do script "cd {os.getcwd()} && ./pbft_main -r node -m local"']
            process = subprocess.Popen(cmd)
            self.processes.append(process)
            print("Node terminal started")
            return True
        except FileNotFoundError:
            print("Terminal.app not found")
            return False
    
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
        print("All processes terminated.")
    
    def run_experiment(self):
        """Run the complete experiment"""
        print("Starting PBFT Local Experiment")
        
        # Set up signal handlers
        signal.signal(signal.SIGINT, self.signal_handler)
        signal.signal(signal.SIGTERM, self.signal_handler)
        
        try:
            # 1. Build the project (includes cleaning logs)
            if not self.build_project():
                return False
            
            # 2. Start node in new terminal first
            if not self.start_node_terminal():
                return False
            
            time.sleep(3)  # Wait 3 seconds before starting client
            
            # 3. Start client in new terminal
            if not self.start_client_terminal():
                return False
            
            print(f"\nExperiment started successfully!")
            print("Two terminals opened:")
            print("  - Terminal 1: Node (./pbft_main -r node -m local)")
            print("  - Terminal 2: Client (./pbft_main -r client -m local)")
            print("Press Ctrl+C to stop the experiment")
            
            # Wait for processes to complete or be interrupted
            while True:
                time.sleep(1)
                # Check if any process has died
                for i, process in enumerate(self.processes):
                    if process.poll() is not None:
                        print(f"Terminal {i+1} has closed")
                
        except KeyboardInterrupt:
            print("\nExperiment interrupted by user")
        except Exception as e:
            print(f"Experiment error: {e}")
        finally:
            self.shutdown()
        
        return True

def main():
    """Main function"""
    experiment = PBFTExperiment()
    success = experiment.run_experiment()
    
    if success:
        print("Experiment completed successfully")
    else:
        print("Experiment failed")
        sys.exit(1)

if __name__ == "__main__":
    main()
