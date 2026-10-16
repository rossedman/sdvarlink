# A container that boots systemd as PID 1 for integration tests.
ARG IMAGE=fedora:42
FROM ${IMAGE}
RUN dnf -y install systemd systemd-resolved systemd-container procps-ng iproute \
    && dnf clean all \
    && systemctl mask systemd-firstboot.service getty.target console-getty.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
