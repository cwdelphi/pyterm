# 运行级镜像:只安装 Python 依赖。
# 代码/前端产物/文档资源全部通过 docker-compose 卷挂载,不 COPY 进镜像,
# 修改代码或文档无需重建容器。
FROM python:3.12-slim

ENV TZ=Asia/Shanghai \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /app

# 仅依赖清单在构建期需要;运行时 app.client 代码由卷挂载
COPY app/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt \
    -i https://pypi.tuna.tsinghua.edu.cn/simple

ENV PORT=5588

EXPOSE 5588

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:5588/api/config', timeout=3)"

CMD ["sh", "-c", "uvicorn app.main:app --host 0.0.0.0 --port ${PORT:-5588}"]