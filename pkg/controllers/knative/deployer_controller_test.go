/*
Copyright 2019 the original author or authors.

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

package knative_test

import (
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/projectriff/system/pkg/apis"
	buildv1alpha1 "github.com/projectriff/system/pkg/apis/build/v1alpha1"
	knativev1alpha1 "github.com/projectriff/system/pkg/apis/knative/v1alpha1"
	knativeservingv1 "github.com/projectriff/system/pkg/apis/thirdparty/knative/serving/v1"
	"github.com/projectriff/system/pkg/controllers/knative"
	rtesting "github.com/projectriff/system/pkg/controllers/testing"
	"github.com/projectriff/system/pkg/controllers/testing/factories"
	"github.com/projectriff/system/pkg/tracker"
)

func TestDeployerReconcile(t *testing.T) {
	testNamespace := "test-namespace"
	testName := "test-deployer"
	testKey := types.NamespacedName{Namespace: testNamespace, Name: testName}
	testImagePrefix := "example.com/repo"
	testSha256 := "cf8b4c69d5460f88530e1c80b8856a70801f31c50b191c8413043ba9b160a43e"
	testImage := fmt.Sprintf("%s/%s@sha256:%s", testImagePrefix, testName, testSha256)
	testAddressURL := "http://internal.local"
	testURL := "http://example.com"

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = buildv1alpha1.AddToScheme(scheme)
	_ = knativev1alpha1.AddToScheme(scheme)
	_ = knativeservingv1.AddToScheme(scheme)

	testDeployer := factories.DeployerKnative().
		NamespaceName(testNamespace, testName).
		Get()

	testApplication := factories.Application().
		NamespaceName(testNamespace, "my-application").
		StatusLatestImage(testImage).
		Get()
	testFunction := factories.Function().
		NamespaceName(testNamespace, "my-function").
		StatusLatestImage(testImage).
		Get()
	testContainer := factories.Container().
		NamespaceName(testNamespace, "my-container").
		StatusLatestImage(testImage).
		Get()

	testConfigurationCreate := factories.KnativeConfiguration().
		ObjectMeta(func(om factories.ObjectMeta) {
			om.Namespace(testNamespace)
			om.GenerateName("%s-deployer-", testName)
			om.ControlledBy(testDeployer, scheme)
			om.AddLabel(knativev1alpha1.DeployerLabelKey, testName)
			om.AddLabel("serving.knative.dev/visibility", "cluster-local")
		}).
		PodTemplateSpec(func(pts factories.PodTemplateSpec) {
			pts.AddLabel(knativev1alpha1.DeployerLabelKey, testName)
			pts.AddLabel("serving.knative.dev/visibility", "cluster-local")
		}).
		UserContainer(func(container *corev1.Container) {
			container.Image = testImage
		}).
		Get()
	testConfigurationGiven := factories.KnativeConfiguration(testConfigurationCreate).
		ObjectMeta(func(om factories.ObjectMeta) {
			om.
				Name("%s001", om.Get().GenerateName).
				Generation(1)
		}).
		StatusObservedGeneration(1).
		Get()

	testRouteCreate := factories.KnativeRoute().
		ObjectMeta(func(om factories.ObjectMeta) {
			om.Namespace(testNamespace)
			om.Name(testName)
			om.ControlledBy(testDeployer, scheme)
			om.AddLabel(knativev1alpha1.DeployerLabelKey, testName)
			om.AddLabel("serving.knative.dev/visibility", "cluster-local")
		}).
		Traffic(
			knativeservingv1.TrafficTarget{
				ConfigurationName: fmt.Sprintf("%s-deployer-%s", testName, "001"),
				Percent:           rtesting.Int64Ptr(100),
			},
		).
		Get()
	testRouteGiven := factories.KnativeRoute(testRouteCreate).
		ObjectMeta(func(om factories.ObjectMeta) {
			om.Generation(1)
		}).
		StatusObservedGeneration(1).
		Get()

	table := rtesting.Table{{
		Name: "deployer does not exist",
		Key:  testKey,
	}, {
		Name: "ignore deleted deployer",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ObjectMeta(func(om factories.ObjectMeta) {
					om.Deleted(1)
				}).
				Get(),
		},
	}, {
		Name: "get deployer failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("get", "Deployer"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ObjectMeta(func(om factories.ObjectMeta) {
					om.Deleted(1)
				}).
				Get(),
		},
		ShouldErr: true,
	}, {
		Name: "create knative resources, from application",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ApplicationRef(testApplication.Name).
				Get(),
			testApplication,
		},
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testApplication, testDeployer, scheme),
		},
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
			testRouteCreate,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "create knative resources, from application, application not found",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ApplicationRef(testApplication.Name).
				Get(),
		},
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testApplication, testDeployer, scheme),
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				Get(),
		},
	}, {
		Name: "create knative resources, from application, get application failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("get", "Application"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ApplicationRef(testApplication.Name).
				Get(),
		},
		ShouldErr: true,
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testApplication, testDeployer, scheme),
		},
	}, {
		Name: "create knative resources, from application, no latest",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ApplicationRef(testApplication.Name).
				Get(),
			factories.Application(testApplication).
				StatusLatestImage("").
				Get(),
		},
		ShouldErr: true,
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testApplication, testDeployer, scheme),
		},
	}, {
		Name: "create knative resources, from function",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				FunctionRef(testFunction.Name).
				Get(),
			testFunction,
		},
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testFunction, testDeployer, scheme),
		},
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
			testRouteCreate,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "create knative resources, from function, function not found",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				FunctionRef(testFunction.Name).
				Get(),
		},
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testFunction, testDeployer, scheme),
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				Get(),
		},
	}, {
		Name: "create knative resources, from function, get function failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("get", "Function"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				FunctionRef(testFunction.Name).
				Get(),
		},
		ShouldErr: true,
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testFunction, testDeployer, scheme),
		},
	}, {
		Name: "create knative resources, from function, no latest",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				FunctionRef(testFunction.Name).
				Get(),
			factories.Function(testFunction).
				StatusLatestImage("").
				Get(),
		},
		ShouldErr: true,
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testFunction, testDeployer, scheme),
		},
	}, {
		Name: "create knative resources, from container",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ContainerRef(testContainer.Name).
				Get(),
			testContainer,
		},
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testContainer, testDeployer, scheme),
		},
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
			testRouteCreate,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "create knative resources, from container, container not found",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ContainerRef(testContainer.Name).
				Get(),
		},
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testContainer, testDeployer, scheme),
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				Get(),
		},
	}, {
		Name: "create knative resources, from container, get container failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("get", "Container"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ContainerRef(testContainer.Name).
				Get(),
		},
		ShouldErr: true,
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testContainer, testDeployer, scheme),
		},
	}, {
		Name: "create knative resources, from container, no latest",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ContainerRef(testContainer.Name).
				Get(),
			factories.Container(testContainer).
				StatusLatestImage("").
				Get(),
		},
		ShouldErr: true,
		ExpectTracks: []rtesting.TrackRequest{
			rtesting.NewTrackRequest(testContainer, testDeployer, scheme),
		},
	}, {
		Name: "create knative resources, from image",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
		},
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
			testRouteCreate,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "create knative resources, create configuration failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("create", "Configuration"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
		},
		ShouldErr: true,
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				Get(),
		},
	}, {
		Name: "create knative resources, create route failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("create", "Route"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
		},
		ShouldErr: true,
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
			testRouteCreate,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				Get(),
		},
	}, {
		Name: "create knative resources, route exists",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("create", "Route", rtesting.InduceFailureOpts{
				Error: apierrs.NewAlreadyExists(schema.GroupResource{}, testName),
			}),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
		},
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
			testRouteCreate,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:    knativev1alpha1.DeployerConditionReady,
						Status:  corev1.ConditionFalse,
						Reason:  "NotOwned",
						Message: `There is an existing Route "test-deployer" that the Deployer does not own.`,
					},
					apis.Condition{
						Type:    knativev1alpha1.DeployerConditionRouteReady,
						Status:  corev1.ConditionFalse,
						Reason:  "NotOwned",
						Message: `There is an existing Route "test-deployer" that the Deployer does not own.`,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				Get(),
		},
	}, {
		Name: "create knative resources, delete extra configurations",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				NamespaceName(testNamespace, "extra-configuration-1").
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				NamespaceName(testNamespace, "extra-configuration-2").
				Get(),
		},
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
			testRouteCreate,
		},
		ExpectDeletes: []rtesting.DeleteRef{
			{Group: "serving.knative.dev", Kind: "Configuration", Namespace: testNamespace, Name: "extra-configuration-1"},
			{Group: "serving.knative.dev", Kind: "Configuration", Namespace: testNamespace, Name: "extra-configuration-2"},
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "create knative resources, delete extra configurations, delete failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("delete", "Configuration"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				NamespaceName(testNamespace, "extra-configuration-1").
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				NamespaceName(testNamespace, "extra-configuration-2").
				Get(),
		},
		ShouldErr: true,
		ExpectDeletes: []rtesting.DeleteRef{
			{Group: "serving.knative.dev", Kind: "Configuration", Namespace: testNamespace, Name: "extra-configuration-1"},
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				Get(),
		},
	}, {
		Name: "create knative resources, delete extra routes",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeRoute(testRouteGiven).
				NamespaceName(testNamespace, "extra-route-1").
				Get(),
			factories.KnativeRoute(testRouteGiven).
				NamespaceName(testNamespace, "extra-route-2").
				Get(),
		},
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
			testRouteCreate,
		},
		ExpectDeletes: []rtesting.DeleteRef{
			{Group: "serving.knative.dev", Kind: "Route", Namespace: testNamespace, Name: "extra-route-1"},
			{Group: "serving.knative.dev", Kind: "Route", Namespace: testNamespace, Name: "extra-route-2"},
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "create knative resources, delete extra routes, delete failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("delete", "Route"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeRoute(testRouteGiven).
				NamespaceName(testNamespace, "extra-route-1").
				Get(),
			factories.KnativeRoute(testRouteGiven).
				NamespaceName(testNamespace, "extra-route-2").
				Get(),
		},
		ShouldErr: true,
		ExpectCreates: []runtime.Object{
			testConfigurationCreate,
		},
		ExpectDeletes: []rtesting.DeleteRef{
			{Group: "serving.knative.dev", Kind: "Route", Namespace: testNamespace, Name: "extra-route-1"},
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				Get(),
		},
	}, {
		Name: "update configuration",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				UserContainer(func(container *corev1.Container) {
					container.Image = "bogus"
				}).
				Get(),
			testRouteGiven,
		},
		ExpectUpdates: []runtime.Object{
			testConfigurationGiven,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "update configuration, listing failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("list", "ConfigurationList"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				UserContainer(func(container *corev1.Container) {
					container.Image = "bogus"
				}).
				Get(),
			testRouteGiven,
		},
		ShouldErr: true,
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				Get(),
		},
	}, {
		Name: "update configuration, update failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("update", "Configuration"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				UserContainer(func(container *corev1.Container) {
					container.Image = "bogus"
				}).
				Get(),
			testRouteGiven,
		},
		ShouldErr: true,
		ExpectUpdates: []runtime.Object{
			testConfigurationGiven,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				Get(),
		},
	}, {
		Name: "update route",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			testConfigurationGiven,
			factories.KnativeRoute(testRouteGiven).
				Traffic().
				Get(),
		},
		ExpectUpdates: []runtime.Object{
			testRouteGiven,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "update route, listing failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("list", "RouteList"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			testConfigurationGiven,
			factories.KnativeRoute(testRouteGiven).
				Traffic().
				Get(),
		},
		ShouldErr: true,
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				Get(),
		},
	}, {
		Name: "update route, update failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("update", "Route"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			testConfigurationGiven,
			factories.KnativeRoute(testRouteGiven).
				Traffic().
				Get(),
		},
		ShouldErr: true,
		ExpectUpdates: []runtime.Object{
			testRouteGiven,
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				Get(),
		},
	}, {
		Name: "update status failed",
		Key:  testKey,
		WithReactors: []rtesting.ReactionFunc{
			rtesting.InduceFailure("update", "Deployer"),
		},
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			testConfigurationGiven,
			testRouteGiven,
		},
		ShouldErr: true,
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "update knative resources, copy annotations and labels",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				ObjectMeta(func(om factories.ObjectMeta) {
					om.AddAnnotation("test-annotation", "test-annotation-value")
					om.AddLabel("test-label", "test-label-value")
				}).
				PodTemplateSpec(func(pts factories.PodTemplateSpec) {
					pts.AddAnnotation("test-annotation-pts", "test-annotation-value")
					pts.AddLabel("test-label-pts", "test-label-value")
				}).
				Image(testImage).
				Get(),
			testConfigurationGiven,
			testRouteGiven,
		},
		ExpectUpdates: []runtime.Object{
			factories.KnativeConfiguration(testConfigurationGiven).
				ObjectMeta(func(om factories.ObjectMeta) {
					om.AddAnnotation("test-annotation", "test-annotation-value")
					om.AddLabel("test-label", "test-label-value")
				}).
				PodTemplateSpec(func(pts factories.PodTemplateSpec) {
					pts.AddAnnotation("test-annotation", "test-annotation-value")
					pts.AddLabel("test-label", "test-label-value")
				}).
				Get(),
			factories.KnativeRoute(testRouteGiven).
				ObjectMeta(func(om factories.ObjectMeta) {
					om.AddLabel("test-label", "test-label-value")
				}).
				Get(),
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "update knative resources, with scale",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				MinScale(1).
				MaxScale(2).
				Get(),
			testConfigurationGiven,
			testRouteGiven,
		},
		ExpectUpdates: []runtime.Object{
			factories.KnativeConfiguration(testConfigurationGiven).
				// TODO figure out which annotation is actually impactful
				ObjectMeta(func(om factories.ObjectMeta) {
					om.AddAnnotation("autoscaling.knative.dev/minScale", "1")
					om.AddAnnotation("autoscaling.knative.dev/maxScale", "2")
				}).
				PodTemplateSpec(func(pts factories.PodTemplateSpec) {
					pts.AddAnnotation("autoscaling.knative.dev/minScale", "1")
					pts.AddAnnotation("autoscaling.knative.dev/maxScale", "2")
				}).
				Get(),
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionUnknown,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionUnknown,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				Get(),
		},
	}, {
		Name: "ready",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				StatusConditions(
					apis.Condition{
						Type:   knativeservingv1.ConfigurationConditionReady,
						Status: corev1.ConditionTrue,
					},
				).
				Get(),
			factories.KnativeRoute(testRouteGiven).
				StatusConditions(
					apis.Condition{
						Type:   knativeservingv1.RouteConditionReady,
						Status: corev1.ConditionTrue,
					},
				).
				StatusAddressURL(testAddressURL).
				StatusURL(testURL).
				Get(),
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionTrue,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionReady,
						Status: corev1.ConditionTrue,
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionTrue,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				StatusAddressURL(testAddressURL).
				StatusURL(testURL).
				Get(),
		},
	}, {
		Name: "not ready, configuration",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				StatusConditions(
					apis.Condition{
						Type:    knativeservingv1.ConfigurationConditionReady,
						Status:  corev1.ConditionFalse,
						Reason:  "TestReason",
						Message: "a human readable message",
					},
				).
				Get(),
			factories.KnativeRoute(testRouteGiven).
				StatusConditions(
					apis.Condition{
						Type:   knativeservingv1.RouteConditionReady,
						Status: corev1.ConditionTrue,
					},
				).
				StatusAddressURL(testAddressURL).
				StatusURL(testURL).
				Get(),
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:    knativev1alpha1.DeployerConditionConfigurationReady,
						Status:  corev1.ConditionFalse,
						Reason:  "TestReason",
						Message: "a human readable message",
					},
					apis.Condition{
						Type:    knativev1alpha1.DeployerConditionReady,
						Status:  corev1.ConditionFalse,
						Reason:  "TestReason",
						Message: "a human readable message",
					},
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionRouteReady,
						Status: corev1.ConditionTrue,
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				StatusAddressURL(testAddressURL).
				StatusURL(testURL).
				Get(),
		},
	}, {
		Name: "not ready, route",
		Key:  testKey,
		GivenObjects: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				Image(testImage).
				Get(),
			factories.KnativeConfiguration(testConfigurationGiven).
				StatusConditions(
					apis.Condition{
						Type:   knativeservingv1.ConfigurationConditionReady,
						Status: corev1.ConditionTrue,
					},
				).
				Get(),
			factories.KnativeRoute(testRouteGiven).
				StatusConditions(
					apis.Condition{
						Type:    knativeservingv1.RouteConditionReady,
						Status:  corev1.ConditionFalse,
						Reason:  "TestReason",
						Message: "a human readable message",
					},
				).
				StatusAddressURL(testAddressURL).
				StatusURL(testURL).
				Get(),
		},
		ExpectStatusUpdates: []runtime.Object{
			factories.DeployerKnative(testDeployer).
				StatusConditions(
					apis.Condition{
						Type:   knativev1alpha1.DeployerConditionConfigurationReady,
						Status: corev1.ConditionTrue,
					},
					apis.Condition{
						Type:    knativev1alpha1.DeployerConditionReady,
						Status:  corev1.ConditionFalse,
						Reason:  "TestReason",
						Message: "a human readable message",
					},
					apis.Condition{
						Type:    knativev1alpha1.DeployerConditionRouteReady,
						Status:  corev1.ConditionFalse,
						Reason:  "TestReason",
						Message: "a human readable message",
					},
				).
				StatusLatestImage(testImage).
				StatusConfigurationRef(testConfigurationGiven.Name).
				StatusRouteRef(testRouteGiven.Name).
				StatusAddressURL(testAddressURL).
				StatusURL(testURL).
				Get(),
		},
	}}

	table.Test(t, scheme, func(t *testing.T, row *rtesting.Testcase, client client.Client, tracker tracker.Tracker, log logr.Logger) reconcile.Reconciler {
		return &knative.DeployerReconciler{
			Client:  client,
			Log:     log,
			Scheme:  scheme,
			Tracker: tracker,
		}
	})
}
