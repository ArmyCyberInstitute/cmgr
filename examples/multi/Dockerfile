# LAUNCH work randomDnsName

FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90 AS builder
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    openssh-client \
    python3 \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir /challenge
COPY finalize.py .

ARG FLAG
ARG SEED

RUN echo $FLAG > flag.txt
RUN ssh-keygen -f id_rsa -N "" -C "asmith@work"

RUN python3 finalize.py



####################
#### Host: work ####
####################
FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90 AS work
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    openssh-server \
    python3 \
    sudo \
    && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /run/sshd

RUN useradd -m -s /bin/bash asmith
RUN useradd -m -s /bin/bash esmythe

COPY apt-get.sudoers /etc/sudoers.d/badsudo

COPY --from=builder set-passwords.sh .
RUN /bin/bash set-passwords.sh && rm set-passwords.sh

RUN mkdir /home/asmith/.ssh
COPY --from=builder id_rsa* /home/asmith/.ssh/
RUN chmod 0600 /home/asmith/.ssh/id_rsa
COPY ssh_config /home/asmith/.ssh/config
RUN chown -R asmith:asmith /home/asmith

CMD ["/usr/sbin/sshd", "-D"]

EXPOSE 22
# PUBLISH 22 AS ssh






###############################
#### Host: random-dns-name ####
###############################
FROM ubuntu:24.04@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90 AS randomDnsName
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    openssh-server \
    python3 \
    sudo \
    && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /run/sshd

RUN useradd -m -s /bin/bash alice

COPY --from=builder flag.txt /home/alice/flag.txt
RUN mkdir /home/alice/.ssh
COPY --from=builder id_rsa.pub /home/alice/.ssh/authorized_keys
RUN chmod 0644 /home/alice/.ssh/authorized_keys
RUN chown -R alice:alice /home/alice

CMD ["/usr/sbin/sshd", "-D"]
