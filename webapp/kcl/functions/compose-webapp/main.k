
import models.io.k8s.api.core.v1 as corev1
import models.com.example.platform.v1alpha1 as platformv1alpha1
import models.io.k8s.api.apps.v1 as appsv1
import models.io.k8s.apimachinery.pkg.apis.meta.v1 as metav1

oxr = option("params").oxr # observed composite resource
_ocds = option("params").ocds # observed composed resources
_dxr = option("params").dxr # desired composite resource
dcds = option("params").dcds # desired composed resources

_xr = platformv1alpha1.WebApp{**oxr}

_labels = {"app.kubernetes.io/name": _xr.metadata.name}
_metadata = lambda name: str -> any {
    {
        annotations = { "krm.kcl.dev/composition-resource-name" = name }
        labels = _labels
    }
}

_items = [
    appsv1.Deployment{
        metadata: _metadata("deployment")
        spec: appsv1.DeploymentSpec{
            replicas: _xr.spec.replicas
            selector: metav1.LabelSelector{
                matchLabels: _labels
            }
            template: corev1.PodTemplateSpec{
                metadata: metav1.ObjectMeta{
                    labels: _labels
                }
                spec: corev1.PodSpec{
                    containers: [
                        corev1.Container{
                            name: _xr.metadata.name
                            image: _xr.spec.image
                            ports: [corev1.ContainerPort{containerPort: p} for p in _xr.spec.ports]
                        }
                    ]
                }
            }
        }
    },
    corev1.Service{
        metadata: _metadata("service")
        spec: corev1.ServiceSpec{
            selector: _labels
            ports: [corev1.ServicePort{
                protocol: "TCP"
                port: p
                targetPort: p
            } for p in _xr.spec.ports]
        }
    }
]
items = _items
