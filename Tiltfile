# -*- mode: Python -*-
update_settings(max_parallel_updates=5, k8s_upsert_timeout_secs=600)

load('ext://helm_resource', 'helm_resource')

helm_resource(
    'envoy-gateway',
    'oci://docker.io/envoyproxy/gateway-helm',
    namespace='envoy-gateway-system',
    flags=[
        '--version=v1.7.2',
        '--create-namespace',
        '--values=deploy/local/envoy-gateway-values.yaml',
    ],
    labels=['gateway'],
)

docker_build('guardrail-adapter-local', '.', dockerfile='Dockerfile')

k8s_yaml(kustomize('deploy/local'))

# Envoy Gateway dynamically creates a data-plane service with a hash suffix
# (e.g. envoy-default-eg-<hash>). Use a label-selector-based port-forward
# so we don't need to hardcode the generated name.
local_resource(
    'envoy-proxy-port-forward',
    serve_cmd='kubectl -n envoy-gateway-system port-forward svc/$(kubectl -n envoy-gateway-system get svc -l gateway.envoyproxy.io/owning-gateway-name=eg -o jsonpath="{.items[0].metadata.name}") 10000:80',
    labels=['gateway'],
    resource_deps=['envoy-gateway'],
)

k8s_resource('echo-mcp', labels=['mcp'])
k8s_resource('presidio', labels=['guardrails'])
k8s_resource('guardrail-adapter', labels=['guardrails'])
