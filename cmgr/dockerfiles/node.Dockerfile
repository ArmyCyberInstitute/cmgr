FROM node:26-bookworm-slim@sha256:2d49d876e96237d76de412761cf05dbfe5aee325cc4406a4d41d5824c5bb8beb AS base

RUN groupadd -r app && useradd -r -d /app -g app app
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*
RUN python3 -m venv /opt/cmgr-venv
ENV PATH="/opt/cmgr-venv/bin:${PATH}"

# End of shared layers for all node challenges

COPY Dockerfile packages.txt* ./
RUN if [ -f packages.txt ]; then \
        apt-get update \
        && xargs -r -a packages.txt apt-get install -y --no-install-recommends \
        && rm -rf /var/lib/apt/lists/*; \
    fi

COPY Dockerfile requirements.txt* ./
RUN if [ -f requirements.txt ]; then \
        pip install --disable-pip-version-check -r requirements.txt; \
    fi

COPY --chown=app:app . /app

WORKDIR /app
USER app:app

ENV PORT=5000
RUN npm ci --omit=dev

# End of share layers for all builds of the same node challenge
FROM base AS challenge

ARG FLAG
ARG SEED

USER root:root
RUN install -d -m 0700 /challenge && \
    echo "{\"flag\":\"$FLAG\"}" > /challenge/metadata.json

RUN find /app \
        \( -path /app/node_modules -o -path /app/.cache \) -prune -o \
        -type f ! -name Dockerfile \
        -exec sed -i \
            -e "s|{{flag}}|$FLAG|g" \
            -e "s|{{seed}}|$SEED|g" \
            {} +

USER app:app
CMD npm start

EXPOSE 5000
# PUBLISH 5000 AS http
