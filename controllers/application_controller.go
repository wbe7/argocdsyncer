/*
Copyright 2022.

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
	"context"
	"errors"
	"reflect"

	appv1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.ru/wbe7/argocdsyncer/config"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	defaultFinalizer = "argoproj.io/finalizer"
	argoFinalizer    = "resources-finalizer.argocd.argoproj.io"
	appNamespace     = config.EnvConfig.ApplicationNamespace
)

// ApplicationReconciler reconciles a Application object
type ApplicationReconciler struct {
	client.Client
	log    *logrus.Entry
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=argoproj.io,resources=applications/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=argoproj.io,resources=applications/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// the Application object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var log = r.log.WithField("application", req.NamespacedName)

	// Получение данных ресурса из k8s
	var desiredResource appv1.Application
	var err = r.Get(ctx, req.NamespacedName, &desiredResource)
	if err != nil {
		if kerrors.IsNotFound(err) {
			log.Debug("Ресурс был удален ранее")
			return ctrl.Result{}, nil
		}
		log.Errorf("< Ошибка при чтении CR Application: %v", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	//Проверка ресурса на совпадение с namespace argocd
	if desiredResource.Namespace == appNamespace {
		log.Debugf("Игнорирую ресурс в namespace %v", appNamespace)
		return ctrl.Result{}, nil
	}

	log.Infof("> Начало обработки ресурса: %v", req.NamespacedName)

	// Проверка удаления
	var deleted = desiredResource.GetDeletionTimestamp() != nil
	if deleted {
		err = r.processDefaultFinalization(log, ctx, &desiredResource, r.finalize)
		if err != nil {
			log.Errorf("Ошибка при финализации ресурса %v: %v", desiredResource.GetName(), err)
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{}, nil
	}

	// Добавление финализатора в ресурс
	if !r.hasDefaultFinalizer(&desiredResource) {
		err = r.InjectDefaultFinalizer(ctx, &desiredResource)
		if err != nil {
			log.Errorf("Ошибка при добавлении финализатора в ресурс %v: %v", desiredResource.GetName(), err)
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{}, nil
	}

	// Прикладная валидация ресурса
	err = r.validate(&desiredResource)
	if err != nil {
		log.Errorf("Ошибка при валидации ресурса %v: %v", desiredResource.GetName(), err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err = r.createOrUpdateApplication(ctx, &desiredResource)
	if err != nil {
		log.Errorf("Ошибка при реконсиляции Application %v: %v", desiredResource.GetName(), err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	//Выход из цикла
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.log = logrus.WithField("controller", "application")
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1.Application{}).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		// For().
		Complete(r)
}

func (r *ApplicationReconciler) hasDefaultFinalizer(resource *appv1.Application) bool {
	return controllerutil.ContainsFinalizer(resource, defaultFinalizer)
}

func (r *ApplicationReconciler) hasArgoFinalizer(resource *appv1.Application) bool {
	return controllerutil.ContainsFinalizer(resource, argoFinalizer)
}

func (r *ApplicationReconciler) InjectDefaultFinalizer(ctx context.Context, resource *appv1.Application) error {
	controllerutil.AddFinalizer(resource, defaultFinalizer)
	return r.Client.Update(ctx, resource)
}

func (r *ApplicationReconciler) processDefaultFinalization(
	log *logrus.Entry,
	ctx context.Context,
	resource *appv1.Application,
	finalizer func(ctx context.Context, resource *appv1.Application) error,
) error {
	if controllerutil.ContainsFinalizer(resource, defaultFinalizer) {
		// Запуск логики финализации ресурса
		if err := finalizer(ctx, resource); err != nil {
			return err
		}

		// Удаление финалайзера и ресурса
		controllerutil.RemoveFinalizer(resource, defaultFinalizer)
		err := r.Client.Update(ctx, resource)
		if err != nil {
			return err
		}
		log.Infof("Успешно удалён финалайзер %v ресурса [%v.%v]", defaultFinalizer, resource.GetName(), resource.GetNamespace())
		if r.hasArgoFinalizer(resource) {
			controllerutil.RemoveFinalizer(resource, argoFinalizer)
			err := r.Client.Update(ctx, resource)
			if err != nil {
				return err
			}
			log.Infof("Успешно удалён финалайзер %v ресурса [%v.%v]", argoFinalizer, resource.GetName(), resource.GetNamespace())
		}
		log.Infof("Успешно удалён ресурс [%v.%v]", resource.GetName(), resource.GetNamespace())
	} else {
		log.Infof("Успешно удалён ресурс [%v.%v]", resource.GetName(), resource.GetNamespace())
	}

	return nil
}

func (r *ApplicationReconciler) finalize(ctx context.Context, resource *appv1.Application) error {
	r.log.Infof("Удаляю целевой Application [%v.%v]", resource.GetName(), resource.GetNamespace())
	desiredApplication, err := generateApplication(resource, appNamespace)
	if err != nil {
		return err
	}
	err = r.Delete(context.TODO(), desiredApplication)
	if err != nil {
		return err
	}
	r.log.Infof("Успешно удален целевой Application [%v.%v]", resource.GetName(), resource.GetNamespace())
	return nil
}

func (r *ApplicationReconciler) validate(resource *appv1.Application) error {
	if resource.Spec.Destination.Namespace != resource.Namespace {
		return errors.New("игнорирую ресурс из-за несовпадения namespace")
	}
	//TODO:Сложная валидация
	return nil
}

func (r *ApplicationReconciler) createOrUpdateApplication(ctx context.Context, resource *appv1.Application) error {
	desiredApplication, err := generateApplication(resource, appNamespace)
	if err != nil {
		return err
	}

	if r.hasArgoFinalizer(resource) {
		controllerutil.AddFinalizer(desiredApplication, argoFinalizer)
	}

	//
	app := &appv1.Application{}

	err = r.Get(ctx, types.NamespacedName{Name: desiredApplication.Name, Namespace: desiredApplication.Namespace}, app)

	if err != nil && kerrors.IsNotFound(err) {
		r.log.Infof("Целевой Application [%v.%v] не создан, создаю...", desiredApplication.GetName(), desiredApplication.GetNamespace())
		err = r.Create(ctx, desiredApplication)
		if err != nil {
			return err
		}
		r.log.Infof("Целевой Application [%v.%v] успешно создан", desiredApplication.GetName(), desiredApplication.GetNamespace())
	} else {
		//Если спецификация отличается, то обновляем
		//TODO: Если финализатор добавляется после создания, то обновления не произойдет
		//Для добавления Argo финализатора нужно пересоздать ресурс
		//Для удаления Argo финализатора вмешательство администраторов
		if !reflect.DeepEqual(app.Spec, desiredApplication.Spec) {
			desiredApplication.ResourceVersion = app.ResourceVersion
			r.log.Infof("Целевой Application [%v.%v] уже создан, обновляю...", desiredApplication.GetName(), desiredApplication.GetNamespace())
			err = r.Update(ctx, desiredApplication)
			if err != nil {
				return err
			}
			r.log.Infof("Целевой Application [%v.%v] успешно обновлен", desiredApplication.GetName(), desiredApplication.GetNamespace())
		}
	}

	return nil
}

func generateApplication(resource *appv1.Application, namespace string) (*appv1.Application, error) {
	return &appv1.Application{
		TypeMeta: metav1.TypeMeta{
			APIVersion: resource.TypeMeta.APIVersion,
			Kind:       resource.Kind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        resource.Name,
			Namespace:   namespace,
			Labels:      resource.Labels,
			Annotations: resource.Annotations,
		},
		Spec: resource.Spec,
	}, nil
}
