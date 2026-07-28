FROM ubuntu:26.04@sha256:3131b4cc82a783df6c9df078f86e01819a13594b865c2cad47bd1bca2b7063bb
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    python3 \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*
RUN python3 -m venv /opt/cmgr-venv
ENV PATH="/opt/cmgr-venv/bin:${PATH}"

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

COPY . /solve
WORKDIR /solve

CMD ["python", "solve.py"]
