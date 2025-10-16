# PBFT系统性能数据可视化管理

本目录包含了用于导出和可视化PBFT系统性能数据的工具，包括TPS（每秒事务数）和Latency（延迟）数据。

## 功能说明

### 1. CSV数据导出
- 系统运行时会自动将TPS、Time和Latency数据导出到`tps_results.csv`文件
- CSV文件包含三列：`Time`（时间，秒）、`TPS`（每秒事务数）和`Latency`（延迟，秒）

### 2. Python绘图脚本
- `plot_tps.py`：读取CSV数据并生成两个图表：
  - TPS vs Time的折线图（保存为`tps_plot.png`）
  - Latency vs Time的折线图（保存为`latency_plot.png`）
- 支持命令行参数自定义输入和输出文件
- 显示详细的统计信息

## 使用方法

### 运行PBFT系统
```bash
# 运行系统（会自动生成tps_results.csv）
go run main.go
```

### 安装Python依赖
```bash
pip install -r requirements.txt
```

### 生成图表
```bash
# 使用默认文件名
python plot_tps.py

# 指定输入和输出文件
python plot_tps.py input.csv tps_output.png latency_output.png
```

## 文件说明

- `tps_results.csv`：系统运行时自动生成的性能数据文件
- `plot_tps.py`：Python绘图脚本
- `requirements.txt`：Python依赖包列表
- `tps_plot.png`：TPS图表文件（默认输出）
- `latency_plot.png`：延迟图表文件（默认输出）

## 图表特性

### TPS图表
- 蓝色折线图显示TPS随时间的变化
- 包含TPS统计信息文本框（最大TPS、平均TPS、总时间）
- 网格线便于读取数值

### Latency图表
- 红色折线图显示延迟随时间的变化
- 包含延迟统计信息文本框（最大延迟、平均延迟、最小延迟）
- 网格线便于读取数值

### 通用特性
- 高分辨率输出（300 DPI）
- 自动调整布局
- 详细的统计信息输出

## 数据格式

CSV文件格式：
```csv
Time,TPS,Latency
0.000000,0.000000,0.000000
1.000000,150.500000,0.006500
2.000000,200.750000,0.005200
...
```

## 注意事项

1. 确保在运行Python脚本前，PBFT系统已经生成了CSV文件
2. 如果CSV文件不存在或缺少必要列，脚本会显示错误信息
3. 图表会同时保存为文件并在屏幕上显示
4. 脚本会自动处理空值和无效数据
5. 延迟数据以秒为单位显示，精度为4位小数

## 统计信息

脚本会输出以下统计信息：
- 总数据点数
- 时间范围
- TPS范围（最小-最大）
- 延迟范围（最小-最大）
- 平均TPS和最大TPS
- 平均延迟和最大延迟