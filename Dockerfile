# 1. 使用官方轻量级 Python 环境作为基础镜像
FROM python:3.11-alpine

# 2. 设置工作目录
WORKDIR /app

# 3. 将本地代码复制到镜像内部 (这就是打包的核心，把代码封印进去)
COPY app.py .

# 4. 安装运行所需的依赖库
RUN pip install flask pyyaml --no-cache-dir

# 5. 声明容器内部监听的端口 (虽然只是声明，但符合最佳实践)
EXPOSE 5000

# 6. 定义容器启动时默认执行的命令
CMD ["python", "app.py"]