FROM ubuntu:26.04@sha256:3131b4cc82a783df6c9df078f86e01819a13594b865c2cad47bd1bca2b7063bb AS base
# Stage 1. base
# This stage is intended to be built from an empty context and ensure a common
# set of dependencies. This is portable across environments and should rarely
# require a rebuild or breaking cache. Is intended to match the default shell
# server environment

ARG DEBIAN_FRONTEND=noninteractive

# challenge building and hosting dependencies
# pulled from ansible/pico-shell/tasks/dependencies.yml
RUN apt-get update && apt-get install -y \
    apt-utils \
    dpkg-dev \
    dpkg \
    fakeroot \
    gcc-multilib \
    iptables-persistent \
    libffi-dev \
    libssl-dev \
    netfilter-persistent \
    nfs-common \
    nodejs \
    php7.2-cli \
    php7.2-sqlite3 \
    python-pip \
    python-virtualenv \
    python3-pip \
    python3.7-dev \
    python3.7-venv \
    python3.7 \
    python3 \
    python-flask \
    socat \
    software-properties-common \
    uwsgi \
    uwsgi-plugin-php \
    uwsgi-plugin-python \
    uwsgi-plugin-python3 \
    xinetd

# additional expected dependencies identified
RUN apt-get update && apt-get install -y \
    sudo \
    git

FROM base AS hacksport
# Stage 2. hacksport (git)
# This stage installs the picoCTF shell_manger/hacksport library from an
# upstream git repository. Everything up until this point could be replaced
# with an "official" picoCTF image.

RUN git clone https://github.com/picoCTF/picoCTF.git \
    && cd picoCTF \
    && git checkout fb09fa2cb745c2db007dc4be8f95e37a1788c830 \
    && python3.7 -m venv /picoCTF-env \
    && . /picoCTF-env/bin/activate \
    && pip install ./picoCTF-shell


# setup the environment shell_manager requires
RUN ln -s /picoCTF-env/bin/shell_manager /usr/local/bin/shell_manager \
    && mkdir -p /opt/hacksports/shared/debs \
    && mkdir -p /opt/hacksports/local \
    && mkdir -p /usr/share/nginx/html/static \
    && chmod 751 /usr/share/nginx/html/static \
    && groupadd problems \
    && useradd hacksports


FROM hacksport AS config
# Stage 3.
# This stage creates default configurations to ensure that shell_manager is
# useable. NOTE: by default these are insecure and should be updated either by
# mapping in the relevant files with a volume, or calling shell_manager config
# /opt/hacksports/local/local_config.json
# /opt/hacksports/shared/shared_config.json

RUN shell_manager config local \
    || shell_manager config shared \
    || shell_manager config local \
    && shell_manager config shared
