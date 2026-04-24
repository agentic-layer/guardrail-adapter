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

# Gateway-API and Envoy Gateway custom resources depend on CRDs installed by
# the envoy-gateway Helm chart. Group them so Tilt waits for that install
# to finish before applying these objects.
k8s_resource(
    new_name='gateway-config',
    objects=[
        'eg:gatewayclass',
        'eg:gateway:default',
        'echo-mcp:httproute:default',
        'allow-default-to-guardrail-adapter:referencegrant:guardrails',
        'guardrail-extproc:envoyextensionpolicy:default',
        'guardrail-route-metadata:envoypatchpolicy:default',
    ],
    resource_deps=['envoy-gateway'],
    labels=['gateway'],
)

# Wait for the data-plane service and pods to be ready (cmd), then run the
# label-selector-based port-forward (serve_cmd) so we don't hardcode the
# generated name. The Envoy Gateway controller only creates the data-plane
# pods after the Gateway CR is applied, so this depends on gateway-config.
local_resource(
    'envoy-proxy-port-forward',
    cmd='''
set -e
echo "Waiting for envoy data-plane service..."
until kubectl -n envoy-gateway-system get svc -l gateway.envoyproxy.io/owning-gateway-name=eg -o jsonpath="{.items[0].metadata.name}" 2>/dev/null | grep -q .; do
  sleep 2
done
echo "Waiting for envoy data-plane pods..."
kubectl -n envoy-gateway-system wait --for=condition=ready pod -l gateway.envoyproxy.io/owning-gateway-name=eg --timeout=300s
''',
    serve_cmd='kubectl -n envoy-gateway-system port-forward svc/$(kubectl -n envoy-gateway-system get svc -l gateway.envoyproxy.io/owning-gateway-name=eg -o jsonpath="{.items[0].metadata.name}") 10000:80',
    labels=['gateway'],
    resource_deps=['gateway-config'],
)

k8s_resource('echo-mcp', labels=['mcp'])
k8s_resource('presidio', labels=['guardrails'])
k8s_resource('guardrail-adapter', labels=['guardrails'])
