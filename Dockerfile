FROM rgpeach10/brew-arm:pr-76

RUN brew install gum kubectl && \
        # installing dns utils, for host package (bind)
        if [ "$TARGETARCH" != "arm64" ]; then brew install helm bind gettext; fi && \
        alias k=kubectl; alias h=helm && \
        if [ "$TARGETARCH" = "arm64" ]; then curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 && chmod 700 get_helm.sh && ./get_helm.sh; fi && \
        brew cleanup --prune=all

RUN brew install devspace go-task && \
        if [ "$TARGETARCH" != "arm64" ]; then brew install yq; fi && \
        if [ "$TARGETARCH" = "arm64" ]; then curl -Lko /usr/bin/yq https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 && chmod +x /usr/bin/yq; fi


RUN brew tap civo/tools && brew install civo && brew cleanup --prune=all

ARG GRAPPLE_CLI_VERSION

RUN brew tap grapple-solution/grapple-go-cli && \
	brew install grapple-go-cli && \
        brew cleanup --prune=all

RUN echo "alias grpl=grapple" >> /home/user/.bashrc && \
	echo "alias k=kubectl" >> /home/user/.bashrc && \
	echo "alias h=helm" >> /home/user/.bashrc

