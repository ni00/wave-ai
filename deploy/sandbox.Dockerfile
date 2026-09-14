# Optional Python + Node agent image. No Docker daemon or Wave credentials.
# docker build -f deploy/sandbox.Dockerfile -t wave-ai-sandbox:local .
FROM node:22-bookworm-slim AS node
FROM python:3.12-slim-bookworm
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
 && apt-get update && apt-get install -y --no-install-recommends bash git ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /workspace /mnt/session/uploads /mnt/session/outputs /mnt/memory /mnt/skills
WORKDIR /workspace
ENV HOME=/workspace LANG=C.UTF-8
CMD ["sleep", "infinity"]
