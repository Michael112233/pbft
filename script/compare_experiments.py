#!/usr/bin/env python3
"""
Compare experiment results for different election methods:
- raft
- normal_case
- round_robin
"""

import csv
import matplotlib
matplotlib.use('Agg')  # Use non-interactive backend
import matplotlib.pyplot as plt
import os
import sys

# Set style
try:
    plt.style.use('seaborn-v0_8-darkgrid')
except:
    plt.style.use('seaborn-darkgrid')
plt.rcParams['figure.figsize'] = (16, 10)
plt.rcParams['font.size'] = 12

def load_data(filepath):
    """Load CSV data and return lists of Time, TPS, Latency"""
    try:
        time_data = []
        tps_data = []
        latency_data = []
        
        with open(filepath, 'r') as f:
            reader = csv.DictReader(f)
            for row in reader:
                try:
                    time_val = float(row['Time'])
                    tps_val = float(row['TPS'])
                    latency_val = float(row['Latency'])
                    time_data.append(time_val)
                    tps_data.append(tps_val)
                    latency_data.append(latency_val)
                except (ValueError, KeyError):
                    continue
        
        return {
            'Time': time_data,
            'TPS': tps_data,
            'Latency': latency_data
        }
    except Exception as e:
        print(f"Error loading {filepath}: {e}")
        return None

def plot_comparison():
    """Generate comparison plots for all three election methods"""
    
    # Data paths
    base_dir = "experiment_result_storage"
    methods = {
        "raft": f"{base_dir}/raft/tps_results.csv",
        "normal_case": f"{base_dir}/normal_case/tps_results.csv",
        "round_robin": f"{base_dir}/round_robin/tps_results.csv"
    }
    
    # Load data
    data = {}
    for method, path in methods.items():
        if os.path.exists(path):
            df = load_data(path)
            if df is not None and len(df.get('Time', [])) > 0:
                data[method] = df
                print(f"Loaded {method}: {len(df['Time'])} data points")
            else:
                print(f"Warning: {method} data is empty or invalid")
        else:
            print(f"Warning: {path} does not exist")
    
    if len(data) == 0:
        print("Error: No data files found!")
        return
    
    # Create figure with subplots
    fig, axes = plt.subplots(2, 2, figsize=(18, 12))
    fig.suptitle('Experiment Results Comparison: Raft vs Normal Case vs Round Robin', 
                 fontsize=16, fontweight='bold', y=0.995)
    
    # Color scheme
    colors = {
        'raft': '#2E86AB',      # Blue
        'normal_case': '#A23B72',  # Purple
        'round_robin': '#F18F01'   # Orange
    }
    
    # Plot 1: TPS over Time
    ax1 = axes[0, 0]
    for method, df in data.items():
        ax1.plot(df['Time'], df['TPS'], 
                label=method.replace('_', ' ').title(), 
                color=colors.get(method, 'gray'),
                linewidth=2, alpha=0.8)
    ax1.set_xlabel('Time (seconds)', fontsize=12)
    ax1.set_ylabel('TPS (Transactions Per Second)', fontsize=12)
    ax1.set_title('Throughput (TPS) Comparison', fontsize=13, fontweight='bold')
    ax1.legend(loc='best', fontsize=11)
    ax1.grid(True, alpha=0.3)
    
    # Plot 2: Latency over Time
    ax2 = axes[0, 1]
    for method, df in data.items():
        ax2.plot(df['Time'], df['Latency'], 
                label=method.replace('_', ' ').title(), 
                color=colors.get(method, 'gray'),
                linewidth=2, alpha=0.8)
    ax2.set_xlabel('Time (seconds)', fontsize=12)
    ax2.set_ylabel('Latency (ms)', fontsize=12)
    ax2.set_title('Latency Comparison', fontsize=13, fontweight='bold')
    ax2.legend(loc='best', fontsize=11)
    ax2.grid(True, alpha=0.3)
    
    # Plot 3: TPS Distribution (Box Plot)
    ax3 = axes[1, 0]
    tps_data_list = [df['TPS'] for df in data.values()]
    labels = [method.replace('_', ' ').title() for method in data.keys()]
    box_plot = ax3.boxplot(tps_data_list, tick_labels=labels, patch_artist=True)
    for patch, method in zip(box_plot['boxes'], data.keys()):
        patch.set_facecolor(colors.get(method, 'gray'))
        patch.set_alpha(0.7)
    ax3.set_ylabel('TPS (Transactions Per Second)', fontsize=12)
    ax3.set_title('TPS Distribution Comparison', fontsize=13, fontweight='bold')
    ax3.grid(True, alpha=0.3, axis='y')
    
    # Plot 4: Latency Distribution (Box Plot)
    ax4 = axes[1, 1]
    latency_data_list = [df['Latency'] for df in data.values()]
    box_plot2 = ax4.boxplot(latency_data_list, tick_labels=labels, patch_artist=True)
    for patch, method in zip(box_plot2['boxes'], data.keys()):
        patch.set_facecolor(colors.get(method, 'gray'))
        patch.set_alpha(0.7)
    ax4.set_ylabel('Latency (ms)', fontsize=12)
    ax4.set_title('Latency Distribution Comparison', fontsize=13, fontweight='bold')
    ax4.grid(True, alpha=0.3, axis='y')
    
    # Add statistics text
    stats_text = "Statistics Summary:\n\n"
    for method, df in data.items():
        method_name = method.replace('_', ' ').title()
        tps_list = df['TPS']
        latency_list = df['Latency']
        avg_tps = sum(tps_list) / len(tps_list) if tps_list else 0
        max_tps = max(tps_list) if tps_list else 0
        avg_latency = sum(latency_list) / len(latency_list) if latency_list else 0
        max_latency = max(latency_list) if latency_list else 0
        stats_text += f"{method_name}:\n"
        stats_text += f"  Avg TPS: {avg_tps:.2f}, Max TPS: {max_tps:.2f}\n"
        stats_text += f"  Avg Latency: {avg_latency:.2f}ms, Max Latency: {max_latency:.2f}ms\n\n"
    
    # Add text box with statistics
    fig.text(0.5, 0.02, stats_text, ha='center', va='bottom', 
             fontsize=10, family='monospace',
             bbox=dict(boxstyle='round', facecolor='wheat', alpha=0.5))
    
    plt.tight_layout(rect=[0, 0.15, 1, 0.98])
    
    # Save figure
    output_path = "experiment_comparison.png"
    plt.savefig(output_path, dpi=300, bbox_inches='tight')
    print(f"\nComparison plot saved to: {output_path}")
    
    # Print summary statistics
    print("\n" + "="*60)
    print("SUMMARY STATISTICS")
    print("="*60)
    for method, df in data.items():
        method_name = method.replace('_', ' ').title()
        time_list = df['Time']
        tps_list = df['TPS']
        latency_list = df['Latency']
        print(f"\n{method_name}:")
        print(f"  Data points: {len(time_list)}")
        if time_list:
            print(f"  Time range: {min(time_list):.2f}s - {max(time_list):.2f}s")
        if tps_list:
            avg_tps = sum(tps_list) / len(tps_list)
            print(f"  TPS - Mean: {avg_tps:.2f}, Max: {max(tps_list):.2f}, Min: {min(tps_list):.2f}")
        if latency_list:
            avg_latency = sum(latency_list) / len(latency_list)
            print(f"  Latency - Mean: {avg_latency:.2f}ms, Max: {max(latency_list):.2f}ms, Min: {min(latency_list):.2f}ms")
    
    # plt.show()  # Commented out for non-interactive environments

if __name__ == "__main__":
    # Change to script directory
    script_dir = os.path.dirname(os.path.abspath(__file__))
    project_root = os.path.dirname(script_dir)
    os.chdir(project_root)
    
    print("Generating experiment comparison plots...")
    print(f"Working directory: {os.getcwd()}")
    plot_comparison()

