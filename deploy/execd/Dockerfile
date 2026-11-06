# Keep every official execd binary; patch only its shared-HOME NSS bootstrap.
FROM sandbox-registry.cn-zhangjiakou.cr.aliyuncs.com/opensandbox/execd:v1.1.0@sha256:6cf7dba2f21f0b536e100563d841ac58a9f31c2b0a081b7ac76796a24d6f47e2
COPY deploy/execd/trust-mitm-ca-nss.sh /tmp/trust-mitm-ca-nss.sh
RUN echo '6646c097e22a6da67c94c6b471a41da54874ab927ef5b52e3de3c5d9adb89289  /bootstrap.sh' | sha256sum -c - \
    && sed -e '/^trust_mitm_ca_nss() {$/r /tmp/trust-mitm-ca-nss.sh' \
           -e '/^trust_mitm_ca_nss() {$/,/^}$/d' /bootstrap.sh > /bootstrap.patched.sh \
    && echo 'e20e74ddb67486f6daebba3e8f2c178b001b0b093a2aef9ddcd801a8ac0eda2d  /bootstrap.patched.sh' | sha256sum -c - \
    && mv /bootstrap.patched.sh /bootstrap.sh \
    && chmod 0755 /bootstrap.sh \
    && rm /tmp/trust-mitm-ca-nss.sh
