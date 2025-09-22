#!/usr/bin/env python3
"""
Local PBFT Experiment Script for Linux
Runs client and multiple nodes in separate terminals
"""

import os
import subprocess
import time
import signal
import sys
import shutil
import re
import requests
import urllib.parse

class PBFTExperiment:
    def __init__(self, node_count=4, headless=False):
        self.processes = []
        self.node_count = node_count
        self.headless = headless
        # Ports that need to be cleaned before starting experiment
        self.required_ports = [20000, 28000, 28100, 28200, 28300]
        # CSV file download URL
        self.csv_url = "https://drive.google.com/file/d/1gIBGcneoUz9jaU48PYCjP6xjWegRlgE-/view"
        self.csv_filename = "len3_data.csv"
        self.data_dir = "data"
        # Linux terminal emulators to try
        self.terminal_emulators = [
            "gnome-terminal",
            "xterm",
            "konsole",
            "xfce4-terminal",
            "mate-terminal",
            "lxterminal",
            "terminator",
            "alacritty",
            "kitty"
        ]
        self.terminal_cmd = self._find_terminal_emulator() if not headless else None
    
    def _find_terminal_emulator(self):
        """Find available terminal emulator on Linux"""
        # Check if DISPLAY is set
        if not os.environ.get('DISPLAY'):
            print("No DISPLAY environment variable found. Running in headless mode.")
            return None
            
        for terminal in self.terminal_emulators:
            try:
                result = subprocess.run(["which", terminal], 
                                      capture_output=True, text=True, timeout=5)
                if result.returncode == 0:
                    print(f"Found terminal emulator: {terminal}")
                    return terminal
            except:
                continue
        
        print("Warning: No supported terminal emulator found. Running in headless mode.")
        return None
    
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
        
    def clean_logs(self):
        """Delete existing logs"""
        print("Cleaning existing logs...")
        if os.path.exists("logs"):
            shutil.rmtree("logs")
            print("Logs directory deleted")
        else:
            print("No logs directory found")
    
    def clean_terminals(self):
        """Close all terminal windows on Linux"""
        print("Closing all terminal windows...")
        try:
            # Kill all terminal processes (more aggressive approach for Linux)
            terminal_processes = [
                "gnome-terminal", "xterm", "konsole", "xfce4-terminal", 
                "mate-terminal", "lxterminal", "terminator", "alacritty", "kitty"
            ]
            
            for terminal in terminal_processes:
                try:
                    subprocess.run(["pkill", "-f", terminal], 
                                 capture_output=True, timeout=5)
                except:
                    pass
            
            print("Terminal processes terminated")
            
            # Wait a moment for terminals to close
            time.sleep(2)
            
        except Exception as e:
            print(f"Error closing terminal windows: {e}")
    
    def clean_ports(self):
        """Clean up processes using required ports"""
        print("Cleaning up processes on required ports...")
        
        try:
            # Use netstat and ss for Linux
            for port in self.required_ports:
                try:
                    # Try ss first (modern Linux)
                    result = subprocess.run(
                        ["ss", "-tulpn", f"sport = :{port}"],
                        capture_output=True, text=True, timeout=10
                    )
                    
                    if result.returncode == 0 and result.stdout.strip():
                        print(f"Found processes using port {port}:")
                        print(result.stdout)
                        
                        # Extract PIDs from ss output
                        for line in result.stdout.split('\n')[1:]:  # Skip header
                            if line.strip() and 'pid=' in line:
                                # Extract PID from line like "pid=12345,fd=3"
                                pid_match = re.search(r'pid=(\d+)', line)
                                if pid_match:
                                    pid = int(pid_match.group(1))
                                    try:
                                        print(f"Killing process {pid}...")
                                        subprocess.run(["kill", "-9", str(pid)], 
                                                     capture_output=True, timeout=5)
                                        print(f"Process {pid} terminated")
                                    except Exception as e:
                                        print(f"Error killing process {pid}: {e}")
                
                except FileNotFoundError:
                    # Fallback to netstat
                    try:
                        result = subprocess.run(
                            ["netstat", "-tulpn", f"| grep :{port}"],
                            shell=True, capture_output=True, text=True, timeout=10
                        )
                        
                        if result.returncode == 0 and result.stdout.strip():
                            print(f"Found processes using port {port}:")
                            print(result.stdout)
                            
                            # Extract PIDs from netstat output
                            for line in result.stdout.split('\n'):
                                if line.strip() and '/' in line:
                                    parts = line.split()
                                    if len(parts) > 6:
                                        pid_program = parts[6]
                                        if '/' in pid_program:
                                            pid = pid_program.split('/')[0]
                                            try:
                                                pid = int(pid)
                                                print(f"Killing process {pid}...")
                                                subprocess.run(["kill", "-9", str(pid)], 
                                                             capture_output=True, timeout=5)
                                                print(f"Process {pid} terminated")
                                            except Exception as e:
                                                print(f"Error killing process {pid}: {e}")
                    except Exception as e:
                        print(f"Error using netstat: {e}")
                
                except Exception as e:
                    print(f"Error checking port {port}: {e}")
            
            # Wait a moment for processes to fully terminate
            time.sleep(2)
            print("Port cleanup completed")
                
        except Exception as e:
            print(f"Error cleaning ports: {e}")
    
    def build_project(self):
        """Build the PBFT project directly"""
        print("Building PBFT project...")
        try:
            # Clean logs folder
            if os.path.exists("logs"):
                shutil.rmtree("logs")
            
            # Create logs directory
            os.makedirs("logs", exist_ok=True)
            print("Created logs directory")
            
            # Build project directly
            # result = subprocess.run(["go", "mod", "tidy"], 
            #                       capture_output=True, text=True, timeout=30)
            # if result.returncode != 0:
            #     print(f"go mod tidy failed: {result.stderr}")
            #     return False
            
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
        """Start client in a new terminal or headless mode"""
        print("Starting client...")
        try:
            current_dir = os.getcwd()
            command = f"cd {current_dir} && ./pbft_main -r client -m local"
            
            if self.headless or not self.terminal_cmd:
                # Run in headless mode (background process)
                print("Starting client in headless mode...")
                process = subprocess.Popen(
                    ["bash", "-c", command],
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    cwd=current_dir
                )
                self.processes.append(process)
                print("Client started in background")
                return True
            else:
                # Use Linux terminal emulator
                print("Starting client in new terminal...")
                if self.terminal_cmd == "gnome-terminal":
                    cmd = ["gnome-terminal", "--", "bash", "-c", f"{command}; exec bash"]
                elif self.terminal_cmd == "xterm":
                    cmd = ["xterm", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "konsole":
                    cmd = ["konsole", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "xfce4-terminal":
                    cmd = ["xfce4-terminal", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "mate-terminal":
                    cmd = ["mate-terminal", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "lxterminal":
                    cmd = ["lxterminal", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "terminator":
                    cmd = ["terminator", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "alacritty":
                    cmd = ["alacritty", "-e", "bash", "-c", f"{command}; exec bash"]
                elif self.terminal_cmd == "kitty":
                    cmd = ["kitty", "bash", "-c", f"{command}; exec bash"]
                else:
                    # Default fallback
                    cmd = ["gnome-terminal", "--", "bash", "-c", f"{command}; exec bash"]
                
                process = subprocess.Popen(cmd)
                self.processes.append(process)
                print("Client terminal started")
                return True
        except FileNotFoundError:
            print(f"Terminal emulator {self.terminal_cmd} not found, falling back to headless mode")
            # Fallback to headless mode
            process = subprocess.Popen(
                ["bash", "-c", command],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                cwd=current_dir
            )
            self.processes.append(process)
            print("Client started in headless mode (fallback)")
            return True
        except Exception as e:
            print(f"Error starting client: {e}")
            return False
    
    def start_node_terminal(self, node_id):
        """Start node in a new terminal or headless mode"""
        print(f"Starting node {node_id}...")
        try:
            current_dir = os.getcwd()
            command = f"cd {current_dir} && ./pbft_main -r node -m local -n {node_id}"
            
            if self.headless or not self.terminal_cmd:
                # Run in headless mode (background process)
                print(f"Starting node {node_id} in headless mode...")
                process = subprocess.Popen(
                    ["bash", "-c", command],
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    cwd=current_dir
                )
                self.processes.append(process)
                print(f"Node {node_id} started in background")
                return True
            else:
                # Use Linux terminal emulator
                print(f"Starting node {node_id} in new terminal...")
                if self.terminal_cmd == "gnome-terminal":
                    cmd = ["gnome-terminal", "--", "bash", "-c", f"{command}; exec bash"]
                elif self.terminal_cmd == "xterm":
                    cmd = ["xterm", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "konsole":
                    cmd = ["konsole", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "xfce4-terminal":
                    cmd = ["xfce4-terminal", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "mate-terminal":
                    cmd = ["mate-terminal", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "lxterminal":
                    cmd = ["lxterminal", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "terminator":
                    cmd = ["terminator", "-e", f"bash -c '{command}; exec bash'"]
                elif self.terminal_cmd == "alacritty":
                    cmd = ["alacritty", "-e", "bash", "-c", f"{command}; exec bash"]
                elif self.terminal_cmd == "kitty":
                    cmd = ["kitty", "bash", "-c", f"{command}; exec bash"]
                else:
                    # Default fallback
                    cmd = ["gnome-terminal", "--", "bash", "-c", f"{command}; exec bash"]
                
                process = subprocess.Popen(cmd)
                self.processes.append(process)
                print(f"Node {node_id} terminal started")
                return True
        except FileNotFoundError:
            print(f"Terminal emulator {self.terminal_cmd} not found, falling back to headless mode")
            # Fallback to headless mode
            process = subprocess.Popen(
                ["bash", "-c", command],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                cwd=current_dir
            )
            self.processes.append(process)
            print(f"Node {node_id} started in headless mode (fallback)")
            return True
        except Exception as e:
            print(f"Error starting node {node_id}: {e}")
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
        print("Starting PBFT Local Experiment (Linux)")
        
        # Set up signal handlers
        signal.signal(signal.SIGINT, self.signal_handler)
        signal.signal(signal.SIGTERM, self.signal_handler)
        
        try:
            # 1. Close all Terminal windows first
            self.clean_terminals()
            
            # 2. Clean up ports
            self.clean_ports()
            
            # 3. Build the project (includes cleaning logs)
            # if not self.build_project():
            #     return False
            
            # 4. Download CSV file after build (must succeed)
            if not self.download_csv_file():
                print("Error: CSV file download failed. Cannot continue without data file.")
                return False
            
            # 5. Start all nodes in separate terminals
            for node_id in range(self.node_count):
                if not self.start_node_terminal(node_id):
                    return False
                time.sleep(1)  # Small delay between starting nodes
            
            time.sleep(3)  # Wait 3 seconds before starting client
            
            # 6. Start client in new terminal
            if not self.start_client_terminal():
                return False
            
            print(f"\nExperiment started successfully!")
            print(f"{self.node_count + 1} terminals opened:")
            for i in range(self.node_count):
                print(f"  - Terminal {i+1}: Node {i} (./pbft_main -r node -m local -n {i})")
            print(f"  - Terminal {self.node_count + 1}: Client (./pbft_main -r client -m local)")
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
    import argparse
    
    parser = argparse.ArgumentParser(description='PBFT Local Experiment Script for Linux')
    parser.add_argument('node_count', type=int, nargs='?', default=4,
                       help='Number of nodes to start (default: 4)')
    parser.add_argument('--headless', action='store_true',
                       help='Run in headless mode (no GUI terminals)')
    
    args = parser.parse_args()
    
    if args.node_count < 1 or args.node_count > 10:
        print("Node count must be between 1 and 10")
        sys.exit(1)
    
    print(f"Starting PBFT experiment with {args.node_count} nodes")
    if args.headless:
        print("Running in headless mode")
    
    experiment = PBFTExperiment(args.node_count, headless=args.headless)
    success = experiment.run_experiment()
    
    if success:
        print("Experiment completed successfully")
    else:
        print("Experiment failed")
        sys.exit(1)

if __name__ == "__main__":
    main()
