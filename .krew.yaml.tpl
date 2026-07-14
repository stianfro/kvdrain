apiVersion: krew.googlecontainertools.github.com/v1alpha2
kind: Plugin
metadata:
  name: kvdrain
spec:
  version: {{ .TagName }}
  homepage: https://github.com/stianfro/kvdrain
  shortDescription: Safely drain Kubernetes nodes running KubeVirt VMIs
  description: |
    kvdrain checks VMI migration safety, evicts normal pods, triggers KubeVirt
    live migration, and reports migration and hotplug state.
  caveats: |
    kvdrain is alpha software. Run `kubectl kvdrain status NODE` before a drain.
    A failed or interrupted drain normally leaves the node cordoned.
  platforms:
    - selector:
        matchLabels:
          os: darwin
          arch: amd64
{{ addURIAndSha "https://github.com/stianfro/kvdrain/releases/download/{{ .TagName }}/kvdrain_{{ .TagName }}_darwin_amd64.tar.gz" .TagName | indent 6 }}
      bin: kvdrain
    - selector:
        matchLabels:
          os: darwin
          arch: arm64
{{ addURIAndSha "https://github.com/stianfro/kvdrain/releases/download/{{ .TagName }}/kvdrain_{{ .TagName }}_darwin_arm64.tar.gz" .TagName | indent 6 }}
      bin: kvdrain
    - selector:
        matchLabels:
          os: linux
          arch: amd64
{{ addURIAndSha "https://github.com/stianfro/kvdrain/releases/download/{{ .TagName }}/kvdrain_{{ .TagName }}_linux_amd64.tar.gz" .TagName | indent 6 }}
      bin: kvdrain
    - selector:
        matchLabels:
          os: linux
          arch: arm64
{{ addURIAndSha "https://github.com/stianfro/kvdrain/releases/download/{{ .TagName }}/kvdrain_{{ .TagName }}_linux_arm64.tar.gz" .TagName | indent 6 }}
      bin: kvdrain
    - selector:
        matchLabels:
          os: windows
          arch: amd64
{{ addURIAndSha "https://github.com/stianfro/kvdrain/releases/download/{{ .TagName }}/kvdrain_{{ .TagName }}_windows_amd64.zip" .TagName | indent 6 }}
      bin: kvdrain.exe
    - selector:
        matchLabels:
          os: windows
          arch: arm64
{{ addURIAndSha "https://github.com/stianfro/kvdrain/releases/download/{{ .TagName }}/kvdrain_{{ .TagName }}_windows_arm64.zip" .TagName | indent 6 }}
      bin: kvdrain.exe
