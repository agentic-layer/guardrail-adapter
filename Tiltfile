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

k8s_resource('envoy-default-eg',
             port_forwards='10000:80',
             labels=['gateway'],
             resource_deps=['envoy-gateway'])

k8s_resource('echo-mcp', labels=['mcp'])
k8s_resource('presidio', labels=['guardrails'])
k8s_resource('guardrail-adapter', labels=['guardrails'])
