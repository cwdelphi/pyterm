FROM mcr.microsoft.com/playwright:v1.48.0-noble
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends python3-pip && rm -rf /var/lib/apt/lists/*
COPY app/requirements.txt /tmp/requirements.txt
RUN pip3 install --break-system-packages -r /tmp/requirements.txt
COPY autotest/package.json autotest/package-lock.json ./
RUN npm ci
COPY autotest/ .
