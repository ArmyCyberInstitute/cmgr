FROM cmgr/hacksport

COPY . /app

ARG FLAG_FORMAT
ARG FLAG
ARG SEED

RUN shell_manager config shared set -f deploy_secret  -v $(echo foo | md5sum | cut -d ' ' -f 1) \
    && shell_manager install /app \
    && shell_manager deploy all -c -i ${SEED} -f ${FLAG_FORMAT}

CMD ["xinetd", "-dontfork"]

EXPOSE 5000
# PUBLISH 5000 AS challenge
