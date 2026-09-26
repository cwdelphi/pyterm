FROM ubuntu:22.04
RUN apt-get update && \
    apt-get install -y --no-install-recommends openssh-server && \
    rm -rf /var/lib/apt/lists/*
RUN mkdir -p /run/sshd
RUN useradd -m -s /bin/bash testuser && echo 'testuser:testpass123' | chpasswd
RUN echo 'root:rootpass123' | chpasswd
RUN sed -i 's/#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && \
    sed -i 's/PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D"]
