/*
Copyright 2019 The Seldon Team.

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

package seldondeployment

import (
	"encoding/json"
	machinelearningv1alpha2 "github.com/seldonio/seldon-operator/pkg/apis/machinelearning/v1alpha2"
	"github.com/seldonio/seldon-operator/pkg/constants"
	"github.com/seldonio/seldon-operator/pkg/utils"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	"strings"
)

func addTFServerContainer(r *ReconcileSeldonDeployment, pu *machinelearningv1alpha2.PredictiveUnit, p *machinelearningv1alpha2.PredictorSpec, deploy *appsv1.Deployment) error {

	if *pu.Implementation == machinelearningv1alpha2.TENSORFLOW_SERVER {

		ty := machinelearningv1alpha2.MODEL
		pu.Type = &ty

		if pu.Endpoint == nil {
			pu.Endpoint = &machinelearningv1alpha2.Endpoint{Type: machinelearningv1alpha2.REST}
		}

		c := utils.GetContainerForDeployment(deploy, pu.Name)
		existing := c != nil
		if !existing {
			c = &v1.Container{
				Name: pu.Name,
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      machinelearningv1alpha2.PODINFO_VOLUME_NAME,
						MountPath: machinelearningv1alpha2.PODINFO_VOLUME_PATH,
					},
				},
			}
		}

		//Add missing fields
		utils.SetImageNameForPrepackContainer(pu, c)
		SetUriParamsForTFServingProxyContainer(pu, c)
		addContainerDefaults(c)

		// Add container to deployment
		if !existing {
			if len(deploy.Spec.Template.Spec.Containers) > 0 {
				deploy.Spec.Template.Spec.Containers = append(deploy.Spec.Template.Spec.Containers, *c)
			} else {
				deploy.Spec.Template.Spec.Containers = []v1.Container{*c}
			}
		}

		tfServingContainer := utils.GetContainerForDeployment(deploy, constants.TFServingContainerName)
		existing = tfServingContainer != nil
		if !existing {
			tfServingContainer = &v1.Container{
				Name:  constants.TFServingContainerName,
				Image: "tensorflow/serving:latest",
				Args: []string{
					"/usr/bin/tensorflow_model_server",
					"--port=2000",
					"--rest_api_port=2001",
					"--model_name=" + pu.Name,
					"--model_base_path=" + DefaultModelLocalMountPath},
				ImagePullPolicy: v1.PullIfNotPresent,
				Ports: []v1.ContainerPort{
					{
						ContainerPort: 2000,
						Protocol:      v1.ProtocolTCP,
					},
					{
						ContainerPort: 2001,
						Protocol:      v1.ProtocolTCP,
					},
				},
			}
		}

		addContainerDefaults(tfServingContainer)

		if !existing {
			deploy.Spec.Template.Spec.Containers = append(deploy.Spec.Template.Spec.Containers, *tfServingContainer)
		}

		_, err := InjectModelInitializer(deploy, tfServingContainer.Name, pu.ModelURI, pu.ServiceAccountName, pu.EnvSecretRefName, r)
		if err != nil {
			return err
		}
	}
	return nil
}

func addModelDefaultServers(r *ReconcileSeldonDeployment, pu *machinelearningv1alpha2.PredictiveUnit, p *machinelearningv1alpha2.PredictorSpec, deploy *appsv1.Deployment) error {
	if *pu.Implementation == machinelearningv1alpha2.SKLEARN_SERVER ||
		*pu.Implementation == machinelearningv1alpha2.XGBOOST_SERVER ||
		*pu.Implementation == machinelearningv1alpha2.MLFLOW_SERVER {

		ty := machinelearningv1alpha2.MODEL
		pu.Type = &ty

		if pu.Endpoint == nil {
			pu.Endpoint = &machinelearningv1alpha2.Endpoint{Type: machinelearningv1alpha2.REST}
		}
		c := utils.GetContainerForDeployment(deploy, pu.Name)
		existing := c != nil
		if !existing {
			c = &v1.Container{
				Name: pu.Name,
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      machinelearningv1alpha2.PODINFO_VOLUME_NAME,
						MountPath: machinelearningv1alpha2.PODINFO_VOLUME_PATH,
					},
				},
			}

		}

		utils.SetImageNameForPrepackContainer(pu, c)

		// Add parameters envvar - point at mount path because initContainer will download
		params := pu.Parameters
		uriParam := machinelearningv1alpha2.Parameter{
			Name:  "model_uri",
			Type:  "STRING",
			Value: DefaultModelLocalMountPath,
		}
		params = append(params, uriParam)
		paramStr, err := json.Marshal(params)
		if err != nil {
			return err
		}
		addContainerDefaults(c)

		if len(params) > 0 {
			if !utils.HasEnvVar(c.Env, machinelearningv1alpha2.ENV_PREDICTIVE_UNIT_PARAMETERS) {
				c.Env = append(c.Env, v1.EnvVar{Name: machinelearningv1alpha2.ENV_PREDICTIVE_UNIT_PARAMETERS, Value: string(paramStr)})
			} else {
				c.Env = utils.SetEnvVar(c.Env, v1.EnvVar{Name: machinelearningv1alpha2.ENV_PREDICTIVE_UNIT_PARAMETERS, Value: string(paramStr)})
			}

		}

		// Add container to deployment
		if !existing {
			if len(deploy.Spec.Template.Spec.Containers) > 0 {
				deploy.Spec.Template.Spec.Containers = append(deploy.Spec.Template.Spec.Containers, *c)
			} else {
				deploy.Spec.Template.Spec.Containers = []v1.Container{*c}
			}
		}

		_, err = InjectModelInitializer(deploy, c.Name, pu.ModelURI, pu.ServiceAccountName, pu.EnvSecretRefName, r.Client)
		if err != nil {
			return err
		}
	}
	return nil
}

func SetUriParamsForTFServingProxyContainer(pu *machinelearningv1alpha2.PredictiveUnit, c *v1.Container) {

	parameters := pu.Parameters

	hasUriParams := false
	if len(pu.Parameters) > 0 {
		for _, paramElement := range pu.Parameters {
			if paramElement.Name == "rest_endpoint" || paramElement.Name == "grpc_endpoint" {

				hasUriParams = true
			}
		}
	}
	if !hasUriParams {
		var uriParam machinelearningv1alpha2.Parameter

		if pu.Endpoint.Type == machinelearningv1alpha2.REST {
			uriParam = machinelearningv1alpha2.Parameter{
				Name:  "rest_endpoint",
				Type:  "STRING",
				Value: "http://0.0.0.0:2001",
			}
		} else {
			uriParam = machinelearningv1alpha2.Parameter{
				Name:  "grpc_endpoint",
				Type:  "STRING",
				Value: "0.0.0.0:2000",
			}

		}

		parameters = append(pu.Parameters, uriParam)

		modelNameParam := machinelearningv1alpha2.Parameter{
			Name:  "model_name",
			Type:  "STRING",
			Value: pu.Name,
		}

		parameters = append(parameters, modelNameParam)

	}

	if len(parameters) > 0 {
		if !utils.HasEnvVar(c.Env, machinelearningv1alpha2.ENV_PREDICTIVE_UNIT_PARAMETERS) {
			c.Env = append(c.Env, v1.EnvVar{Name: machinelearningv1alpha2.ENV_PREDICTIVE_UNIT_PARAMETERS, Value: utils.GetPredictiveUnitAsJson(parameters)})
		} else {
			c.Env = utils.SetEnvVar(c.Env, v1.EnvVar{Name: machinelearningv1alpha2.ENV_PREDICTIVE_UNIT_PARAMETERS, Value: utils.GetPredictiveUnitAsJson(parameters)})
		}

	}
}

func addContainerDefaults(c *v1.Container) {
	// Add some defaults for easier diffs
	if c.TerminationMessagePath == "" {
		c.TerminationMessagePath = "/dev/termination-log"
	}
	if c.TerminationMessagePolicy == "" {
		c.TerminationMessagePolicy = corev1.TerminationMessageReadFile
	}

	if c.ImagePullPolicy == "" {
		c.ImagePullPolicy = corev1.PullIfNotPresent
	}

	if c.SecurityContext != nil && c.SecurityContext.ProcMount == nil {
		var procMount = corev1.DefaultProcMount
		c.SecurityContext.ProcMount = &procMount
	}
}

func createStandaloneModelServers(r *ReconcileSeldonDeployment, mlDep *machinelearningv1alpha2.SeldonDeployment, p *machinelearningv1alpha2.PredictorSpec, c *components, pu *machinelearningv1alpha2.PredictiveUnit) error {

	// some predictors have no podSpec so this could be nil
	sPodSpec := utils.GetSeldonPodSpecForPredictiveUnit(p, pu.Name)

	depName := machinelearningv1alpha2.GetDeploymentName(mlDep, *p, sPodSpec)

	var deploy *appsv1.Deployment
	existing := false
	for i := 0; i < len(c.deployments); i++ {
		d := c.deployments[i]
		if strings.Compare(d.Name, depName) == 0 {
			deploy = d
			existing = true
			break
		}
	}

	// might not be a Deployment yet - if so we have to create one
	if deploy == nil {
		seldonId := machinelearningv1alpha2.GetSeldonDeploymentName(mlDep)
		deploy = createDeploymentWithoutEngine(depName, seldonId, sPodSpec, p, mlDep)
	}

	if err := addModelDefaultServers(r, pu, p, deploy); err != nil {
		return err
	}
	if err := addTFServerContainer(r, pu, p, deploy); err != nil {
		return err
	}

	if !existing {

		// this is a new deployment so its containers won't have a containerService
		for k := 0; k < len(deploy.Spec.Template.Spec.Containers); k++ {
			con := &deploy.Spec.Template.Spec.Containers[k]

			//checking for con.Name != "" is a fallback check that we haven't got an empty/nil container as name is required
			if con.Name != EngineContainerName && con.Name != constants.TFServingContainerName && con.Name != "" {
				svc := createContainerService(deploy, *p, mlDep, con, *c)
				c.services = append(c.services, svc)
			}
		}
		if len(deploy.Spec.Template.Spec.Containers) > 0 && deploy.Spec.Template.Spec.Containers[0].Name != "" {
			// Add deployment, provided we have a non-empty spec
			c.deployments = append(c.deployments, deploy)
		}
	}

	for i := 0; i < len(pu.Children); i++ {
		if err := createStandaloneModelServers(r, mlDep, p, c, &pu.Children[i]); err != nil {
			return err
		}
	}
	return nil
}
