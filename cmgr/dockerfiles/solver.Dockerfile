FROM ubuntu:25.10@sha256:7cc5e35f6567ee8c66d2abb4aab0fd866669e6207c237c3a8f0947a5c7f17092
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
