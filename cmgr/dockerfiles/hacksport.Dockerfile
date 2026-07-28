FROM ubuntu:26.04@sha256:3131b4cc82a783df6c9df078f86e01819a13594b865c2cad47bd1bca2b7063bb AS runtime

ARG DEBIAN_FRONTEND=noninteractive

COPY .cmgr/hacksport_compat/cmgr_hacksport/install_dependencies.py \
    /opt/cmgr-hacksport/cmgr_hacksport/install_dependencies.py
COPY .cmgr/problem.json /tmp/cmgr-hacksport/problem.json
COPY .cmgr/packages.txt /tmp/cmgr-hacksport/packages.txt
COPY .cmgr/requirements.txt /tmp/cmgr-hacksport/requirements.txt
COPY .cmgr/install_dependencies /tmp/cmgr-hacksport/install_dependencies

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        python3 \
        python3-venv \
    && rm -rf /var/lib/apt/lists/*

RUN python3 /opt/cmgr-hacksport/cmgr_hacksport/install_dependencies.py \
        prepare \
        --metadata /tmp/cmgr-hacksport/problem.json \
        --packages /tmp/cmgr-hacksport/packages.txt \
        --requirements /tmp/cmgr-hacksport/requirements.txt \
        --output /tmp/cmgr-hacksport/prepared \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        php-cli \
        socat \
    && xargs -r apt-get install -y --no-install-recommends \
        < /tmp/cmgr-hacksport/prepared/apt-packages.txt \
    && python3 -m venv /opt/cmgr-hacksport-venv \
    && /opt/cmgr-hacksport-venv/bin/python -m pip install --no-cache-dir \
        Flask==3.1.3 \
        Jinja2==3.1.6 \
    && /opt/cmgr-hacksport-venv/bin/python -m pip install --no-cache-dir \
        -r /tmp/cmgr-hacksport/prepared/requirements.txt \
    && PATH="/opt/cmgr-hacksport-venv/bin:${PATH}" \
        bash /tmp/cmgr-hacksport/install_dependencies \
    && apt-get purge -y build-essential \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* /root/.cache /tmp/cmgr-hacksport/prepared

COPY .cmgr/hacksport_compat /opt/cmgr-hacksport

ENV PATH="/opt/cmgr-hacksport-venv/bin:${PATH}"
ENV PYTHONPATH="/opt/cmgr-hacksport"

RUN groupadd --gid 10001 challenge \
    && useradd --uid 10001 --gid challenge --home-dir /app \
        --no-create-home --shell /usr/sbin/nologin challenge

FROM runtime AS builder

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc g++ make libc6-dev \
    && if [ "$(dpkg --print-architecture)" = "amd64" ]; then \
        apt-get install -y --no-install-recommends gcc-multilib g++-multilib; \
       fi \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY . .

ARG FLAG_FORMAT
ARG FLAG
ARG SEED

ENV FLAG_FORMAT="${FLAG_FORMAT}"
ENV FLAG="${FLAG}"
ENV SEED="${SEED}"

RUN /opt/cmgr-hacksport-venv/bin/python \
        /opt/cmgr-hacksport/cmgr_hacksport/runner.py \
        --source /src \
        --metadata /tmp/cmgr-hacksport/problem.json \
        --application /app \
        --challenge-output /challenge

FROM runtime AS challenge

COPY --from=builder /app /app
COPY --from=builder /challenge /challenge

WORKDIR /app
USER challenge:challenge

ENTRYPOINT ["/opt/cmgr-hacksport-venv/bin/python", "/opt/cmgr-hacksport/cmgr_hacksport/launch.py"]

EXPOSE 5000
# PUBLISH 5000 AS challenge
