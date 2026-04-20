FROM python:3.12-slim

WORKDIR /app

RUN pip install redis --no-cache-dir

COPY runner.py .

ENV REDIS_URL=redis://redis-svc:6379
ENV CLOUDOS_TASK_ID=unknown
ENV CLOUDOS_COMMAND="echo hello"

CMD ["python", "runner.py"]
