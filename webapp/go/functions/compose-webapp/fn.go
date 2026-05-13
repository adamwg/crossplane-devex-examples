package main

import (
	"context"
	"encoding/json"

	"dev.crossplane.io/models/com/example/platform/v1alpha1"
	appsv1 "dev.crossplane.io/models/io/k8s/apps/v1"
	metav1 "dev.crossplane.io/models/io/k8s/core/meta/v1"
	corev1 "dev.crossplane.io/models/io/k8s/core/v1"
	utilv1 "dev.crossplane.io/models/io/k8s/util/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/function-sdk-go/logging"
	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"
	"k8s.io/utils/ptr"
)

// Function is your composition function.
type Function struct {
	fnv1.UnimplementedFunctionRunnerServiceServer

	log logging.Logger
}

// RunFunction runs the Function.
func (f *Function) RunFunction(_ context.Context, req *fnv1.RunFunctionRequest) (*fnv1.RunFunctionResponse, error) {
	f.log.Info("Running function", "tag", req.GetMeta().GetTag())
	rsp := response.To(req, response.DefaultTTL)

	observedComposite, err := request.GetObservedCompositeResource(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot get xr"))
		return rsp, nil
	}

	var xr v1alpha1.WebApp
	if err := convertViaJSON(&xr, observedComposite.Resource); err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot convert xr"))
		return rsp, nil
	}

	// We'll collect our desired composed resources into this map, then convert
	// them to the SDK's types and set them in the response when we return.
	desiredComposed := make(map[resource.Name]any)
	defer func() {
		desiredComposedResources, err := request.GetDesiredComposedResources(req)
		if err != nil {
			response.Fatal(rsp, errors.Wrap(err, "cannot get desired resources"))
			return
		}

		for name, obj := range desiredComposed {
			c := composed.New()
			if err := convertViaJSON(c, obj); err != nil {
				response.Fatal(rsp, errors.Wrapf(err, "cannot convert %s to unstructured", name))
				return
			}
			dc := &resource.DesiredComposed{Resource: c}

			// Check if this resource should be marked as ready
			if c.GetAnnotations()["go.upbound.io/ready"] == "True" {
				dc.Ready = resource.ReadyTrue
			}

			desiredComposedResources[name] = dc
		}

		if err := response.SetDesiredComposedResources(rsp, desiredComposedResources); err != nil {
			response.Fatal(rsp, errors.Wrap(err, "cannot set desired resources"))
			return
		}
	}()

	var (
		cports []corev1.ContainerPort
		sports []corev1.ServicePort
	)
	if xr.Spec.Ports != nil {
		cports = make([]corev1.ContainerPort, len(*xr.Spec.Ports))
		sports = make([]corev1.ServicePort, len(*xr.Spec.Ports))

		for i, p := range *xr.Spec.Ports {
			cports[i] = corev1.ContainerPort{
				ContainerPort: ptr.To(int32(p)),
			}
			sports[i] = corev1.ServicePort{
				Port:       ptr.To(int32(p)),
				TargetPort: new(utilv1.IntOrString),
			}
			_ = sports[i].TargetPort.FromInt(p)
		}
	}

	// Create Deployment
	deployment := &appsv1.Deployment{
		APIVersion: ptr.To(appsv1.DeploymentAPIVersionAppsV1),
		Kind:       ptr.To(appsv1.DeploymentKindDeployment),
		Metadata: &metav1.ObjectMeta{
			Name:      xr.Metadata.Name,
			Namespace: xr.Metadata.Namespace,
			Labels: &map[string]string{
				"app.kubernetes.io/name": *xr.Metadata.Name,
			},
		},
		Spec: &appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(*xr.Spec.Replicas)),
			Selector: &metav1.LabelSelector{
				MatchLabels: &map[string]string{
					"app.kubernetes.io/name": *xr.Metadata.Name,
				},
			},
			// ToDo(haarchri): remove this
			Strategy: &appsv1.IoK8SApiAppsV1DeploymentStrategy{},
			Template: &corev1.PodTemplateSpec{
				Metadata: &metav1.ObjectMeta{
					Labels: &map[string]string{
						"app.kubernetes.io/name": *xr.Metadata.Name,
					},
				},
				Spec: &corev1.PodSpec{
					Containers: &[]corev1.Container{{
						Name:  xr.Metadata.Name,
						Image: xr.Spec.Image,
						Ports: &cports,
					}},
				},
			},
		},
	}

	desiredComposed["deployment"] = deployment

	// Create Service if enabled
	service := &corev1.Service{
		APIVersion: ptr.To(corev1.ServiceAPIVersionV1),
		Kind:       ptr.To(corev1.ServiceKindService),
		Metadata: &metav1.ObjectMeta{
			Name:      xr.Metadata.Name,
			Namespace: xr.Metadata.Namespace,
		},
		Spec: &corev1.ServiceSpec{
			Selector: &map[string]string{
				"app.kubernetes.io/name": *xr.Metadata.Name,
			},
			Ports: &sports,
		},
		// ToDo(haarchri): remove this
		Status: &corev1.ServiceStatus{
			LoadBalancer: &corev1.LoadBalancerStatus{},
		},
	}

	desiredComposed["service"] = service

	return rsp, nil
}

func convertViaJSON(to, from any) error {
	bs, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(bs, to)
}
