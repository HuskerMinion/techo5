FROM mcr.microsoft.com/playwright:v1.64.1-noble
RUN apt-get update && apt-get install -y --no-install-recommends iptables && rm -rf /var/lib/apt/lists/*
WORKDIR /app
RUN npm init -y >/dev/null && npm install --omit=dev playwright@1.64.1
COPY server.js entrypoint.sh /app/
RUN chmod +x /app/entrypoint.sh
EXPOSE 8891
ENTRYPOINT ["/app/entrypoint.sh"]
