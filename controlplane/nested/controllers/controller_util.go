/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"text/template"

	openuri "github.com/utahta/go-openuri"

	"github.com/go-logr/logr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlcli "sigs.k8s.io/controller-runtime/pkg/client"

	controlplanev1 "sigs.k8s.io/cluster-api-provider-nested/controlplane/nested/api/v1alpha4"
	addonv1alpha1 "sigs.k8s.io/kubebuilder-declarative-pattern/pkg/patterns/addon/pkg/apis/v1alpha1"
)

// +kubebuilder:rbac:groups="";apps,resources=services;statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="";apps,resources=services/status;statefulsets/status,verbs=get;update;patch

// createNestedComponentSts will create the StatefulSet that runs the
// NestedComponent
func createNestedComponentSts(ctx context.Context,
	cli ctrlcli.Client, ncMeta metav1.ObjectMeta,
	ncSpec controlplanev1.NestedComponentSpec,
	ncKind controlplanev1.ComponentKind,
	controlPlaneName, clusterName, templatePath string, log logr.Logger) error {
	ncSts := &appsv1.StatefulSet{}
	ncSvc := &corev1.Service{}
	// Setup the ownerReferences for all objects
	or := metav1.NewControllerRef(&ncMeta,
		controlplanev1.GroupVersion.WithKind(string(ncKind)))

	// 1. Using the template defined by version/channel to create the
	// StatefulSet and the Service
	// TODO check the template version/channel, if not set, use the default.
	if ncSpec.Version != "" && ncSpec.Channel != "" {
		panic("NOT IMPLEMENT YET")
	}

	log.V(4).Info("The Version and Channel are not set, " +
		"will use the default template.")
	if err := genStatefulSetObject(templatePath, ncMeta, ncSpec, ncKind, controlPlaneName, clusterName, log, ncSts); err != nil {
		return fmt.Errorf("fail to generate the Statefulset object: %v", err)
	}

	if ncKind != controlplanev1.ControllerManager {
		// no need to create the service for the NestedControllerManager
		if err := genServiceObject(templatePath, ncMeta, ncSpec, ncKind, controlPlaneName, clusterName, log, ncSvc); err != nil {
			return fmt.Errorf("fail to generate the Service object: %v", err)
		}

		ncSvc.SetOwnerReferences([]metav1.OwnerReference{*or})
		if err := cli.Create(ctx, ncSvc); err != nil {
			return err
		}
		log.Info("successfully create the service for the StatefulSet",
			"component", ncKind)
	}

	// 2. set the NestedComponent object as the owner of the StatefulSet
	ncSts.SetOwnerReferences([]metav1.OwnerReference{*or})

	// 4. create the NestedComponent StatefulSet
	return cli.Create(ctx, ncSts)
}

// genServiceObject generates the Service object corresponding to the
// NestedComponent
func genServiceObject(
	templatePath string,
	ncMeta metav1.ObjectMeta,
	ncSpec controlplanev1.NestedComponentSpec, ncKind controlplanev1.ComponentKind,
	controlPlaneName, clusterName string, log logr.Logger, svc *corev1.Service) error {
	var templateURL string
	if ncSpec.Version == "" && ncSpec.Channel == "" {
		switch ncKind {
		case controlplanev1.APIServer:
			templateURL = templatePath + defaultKASServiceURL
		case controlplanev1.Etcd:
			templateURL = templatePath + defaultEtcdServiceURL
		default:
			panic("Unreachable")
		}
	} else {
		panic("NOT IMPLEMENT YET")
	}
	svcTmpl, err := fetchTemplate(templateURL)
	if err != nil {
		return fmt.Errorf("fail to fetch the default template "+
			"for the %s service: %v", ncKind, err)
	}

	templateCtx := getTemplateArgs(ncMeta, controlPlaneName, clusterName)

	svcStr, err := substituteTemplate(templateCtx, svcTmpl)
	if err != nil {
		return fmt.Errorf("fail to substitute the default template "+
			"for the nestedetcd Service: %v", err)
	}
	if err := yamlToObject([]byte(svcStr), svc); err != nil {
		return fmt.Errorf("fail to convert yaml file to Serivce: %v", err)
	}
	log.Info("deserialize yaml to runtime object(Service)")

	return nil
}

// genStatefulSetObject generates the StatefulSet object corresponding to the
// NestedComponent
func genStatefulSetObject(
	templatePath string,
	ncMeta metav1.ObjectMeta,
	ncSpec controlplanev1.NestedComponentSpec,
	ncKind controlplanev1.ComponentKind, controlPlaneName, clusterName string,
	log logr.Logger, ncSts *appsv1.StatefulSet) error {
	var templateURL string
	if ncSpec.Version == "" && ncSpec.Channel == "" {
		log.V(4).Info("The Version and Channel are not set, " +
			"will use the default template.")
		switch ncKind {
		case controlplanev1.APIServer:
			templateURL = templatePath + defaultKASStatefulSetURL
		case controlplanev1.Etcd:
			templateURL = templatePath + defaultEtcdStatefulSetURL
		case controlplanev1.ControllerManager:
			templateURL = templatePath + defaultKCMStatefulSetURL
		default:
			panic("Unreachable")
		}
	} else {
		panic("NOT IMPLEMENT YET")
	}

	// 1 fetch the statefulset template
	stsTmpl, err := fetchTemplate(templateURL)
	if err != nil {
		return fmt.Errorf("fail to fetch the default template "+
			"for the %s StatefulSet: %v", ncKind, err)
	}
	// 2 substitute the statefulset template
	templateCtx := getTemplateArgs(ncMeta, controlPlaneName, clusterName)
	stsStr, err := substituteTemplate(templateCtx, stsTmpl)
	if err != nil {
		return fmt.Errorf("fail to substitute the default template "+
			"for the %s StatefulSet: %v", ncKind, err)
	}
	// 3 deserialize the yaml string to the StatefulSet object

	if err := yamlToObject([]byte(stsStr), ncSts); err != nil {
		return fmt.Errorf("fail to convert yaml file to StatefulSet: %v", err)
	}
	log.V(5).Info("deserialize yaml to runtime object(StatefulSet)")

	// 5 apply NestedComponent.Spec.Resources and NestedComponent.Spec.Replicas
	// to the NestedComponent StatefulSet
	for i := range ncSts.Spec.Template.Spec.Containers {
		ncSts.Spec.Template.Spec.Containers[i].Resources =
			ncSpec.Resources
	}
	if ncSpec.Replicas != 0 {
		ncSts.Spec.Replicas = &ncSpec.Replicas
	}
	log.V(5).Info("The NestedEtcd StatefulSet's Resources and "+
		"Replicas fields are set",
		"StatefulSet", ncSts.GetName())

	// 6 set the "--initial-cluster" command line flag for the Etcd container
	if ncKind == controlplanev1.Etcd {
		icaVal := genInitialClusterArgs(1, clusterName, clusterName, ncMeta.GetNamespace())
		stsArgs := append(ncSts.Spec.Template.Spec.Containers[0].Args,
			"--initial-cluster", icaVal)
		ncSts.Spec.Template.Spec.Containers[0].Args = stsArgs
		log.V(5).Info("The '--initial-cluster' command line option is set")
	}

	// 7 TODO validate the patch and apply it to the template.
	return nil
}

func getTemplateArgs(ncMeta metav1.ObjectMeta, controlPlaneName, clusterName string) map[string]string {
	return map[string]string{
		"componentName":      ncMeta.GetName(),
		"componentNamespace": ncMeta.GetNamespace(),
		"clusterName":        clusterName,
		"controlPlaneName":   controlPlaneName,
	}
}

// yamlToObject deserialize the yaml to the runtime object
func yamlToObject(yamlContent []byte, obj runtime.Object) error {
	decode := serializer.NewCodecFactory(scheme.Scheme).
		UniversalDeserializer().Decode
	_, _, err := decode(yamlContent, nil, obj)
	if err != nil {
		return err
	}
	return nil
}

// substituteTemplate substitutes the template contents with the context
func substituteTemplate(context interface{}, tmpl string) (string, error) {
	t, tmplPrsErr := template.New("test").
		Option("missingkey=zero").Parse(tmpl)
	if tmplPrsErr != nil {
		return "", tmplPrsErr
	}
	writer := bytes.NewBuffer([]byte{})
	if err := t.Execute(writer, context); nil != err {
		return "", err
	}

	return writer.String(), nil
}

// fetchTemplate fetches the component template through the tmplateURL
func fetchTemplate(templateURL string) (string, error) {
	rep, err := openuri.Open(templateURL)
	if err != nil {
		return "", err
	}
	defer rep.Close()

	bodyBytes, err := ioutil.ReadAll(rep)
	if err != nil {
		return "", err
	}

	return string(bodyBytes), nil
}

// getOwner gets the ownerreference of the NestedComponent
func getOwner(ncMeta metav1.ObjectMeta) metav1.OwnerReference {
	owners := ncMeta.GetOwnerReferences()
	if len(owners) == 0 {
		return metav1.OwnerReference{}
	}
	for _, owner := range owners {
		if owner.APIVersion == controlplanev1.GroupVersion.String() &&
			owner.Kind == "NestedControlPlane" {
			return owner
		}
	}
	return metav1.OwnerReference{}
}

// genAPIServerSvcRef generates the ObjectReference that points to the
// APISrver service
func genAPIServerSvcRef(cli ctrlcli.Client,
	nkas controlplanev1.NestedAPIServer, clusterName string) (corev1.ObjectReference, error) {
	var (
		svc    corev1.Service
		objRef corev1.ObjectReference
	)
	if err := cli.Get(context.TODO(), types.NamespacedName{
		Namespace: nkas.GetNamespace(),
		Name:      fmt.Sprintf("%s-apiserver", clusterName),
	}, &svc); err != nil {
		return objRef, err
	}
	objRef = genObjRefFromObj(&svc)
	return objRef, nil
}

// genObjRefFromObj generates the ObjectReference of the given object
func genObjRefFromObj(obj ctrlcli.Object) corev1.ObjectReference {
	return corev1.ObjectReference{
		Kind:       obj.GetObjectKind().GroupVersionKind().Kind,
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		UID:        obj.GetUID(),
		APIVersion: obj.GetObjectKind().GroupVersionKind().GroupVersion().Version,
	}
}

func IsComponentReady(status addonv1alpha1.CommonStatus) bool {
	return status.Phase == string(controlplanev1.Ready)
}
