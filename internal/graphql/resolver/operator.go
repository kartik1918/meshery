package resolver

import (
	"context"

	"github.com/layer5io/meshery/internal/graphql/model"
	"github.com/layer5io/meshery/models"
	"github.com/layer5io/meshkit/utils/broadcast"
	mesherykube "github.com/layer5io/meshkit/utils/kubernetes"
)

func (r *Resolver) changeOperatorStatus(ctx context.Context, provider models.Provider, status model.Status) (model.Status, error) {
	delete := true

	// Tell operator status subscription that operation is starting
	// r.operatorSyncChannel <- true
	r.Broadcast.Submit(broadcast.BroadcastMessage{
		Type:    broadcast.OperatorSyncChannel,
		Message: true,
	})

	if status == model.StatusEnabled {
		r.Log.Info("Installing Operator")
		delete = false
	}

	if r.Config.KubeClient.KubeClient == nil {
		r.Log.Error(ErrNilClient)
		// r.operatorSyncChannel <- false
		r.Broadcast.Submit(broadcast.BroadcastMessage{
			Type:    broadcast.OperatorSyncChannel,
			Message: false,
		})
		return model.StatusUnknown, ErrNilClient
	}

	go func(del bool, kubeclient *mesherykube.Client) {
		err := model.Initialize(kubeclient, del)
		if err != nil {
			r.Log.Error(err)
			// r.operatorSyncChannel <- false
			r.Broadcast.Submit(broadcast.BroadcastMessage{
				Type:    broadcast.OperatorSyncChannel,
				Message: false,
			})
			return
		}
		r.Log.Info("Operator operation executed")

		if !del {
			_, err := r.resyncCluster(context.TODO(), provider, &model.ReSyncActions{
				ReSync:  "false",
				ClearDb: "true",
			})
			if err != nil {
				r.Log.Error(err)
				// r.operatorSyncChannel <- false
				r.Broadcast.Submit(broadcast.BroadcastMessage{
					Type:    broadcast.OperatorSyncChannel,
					Message: false,
				})
				return
			}

			endpoint, err := model.SubscribeToBroker(provider, kubeclient, r.brokerChannel, r.BrokerConn)
			r.Log.Debug("Endpoint: ", endpoint)
			if err != nil {
				r.Log.Error(err)
				// r.operatorSyncChannel <- false
				r.Broadcast.Submit(broadcast.BroadcastMessage{
					Type:    broadcast.OperatorSyncChannel,
					Message: false,
				})
				return
			}
			r.Log.Info("Connected to broker at:", endpoint)
		}

		// installMeshsync
		err = model.RunMeshSync(kubeclient, del)
		if err != nil {
			r.Log.Error(err)
			// r.operatorSyncChannel <- false
			r.Broadcast.Submit(broadcast.BroadcastMessage{
				Type:    broadcast.OperatorSyncChannel,
				Message: false,
			})
			return
		}
		r.Log.Info("Meshsync operation executed")

		// r.operatorChannel <- &model.OperatorStatus{
		// 	Status: status,
		// }

		// r.operatorSyncChannel <- false
		r.Broadcast.Submit(broadcast.BroadcastMessage{
			Type:    broadcast.OperatorSyncChannel,
			Message: false,
		})
	}(delete, r.Config.KubeClient)

	return model.StatusProcessing, nil
}

func (r *Resolver) getOperatorStatus(ctx context.Context, provider models.Provider) (*model.OperatorStatus, error) {
	status := model.StatusUnknown
	version := string(model.StatusUnknown)
	if r.Config.KubeClient == nil {
		return nil, ErrMesheryClient(nil)
	}

	name, version, err := model.GetOperator(r.Config.KubeClient)
	if err != nil {
		r.Log.Error(err)
		return &model.OperatorStatus{
			Status: status,
			Error: &model.Error{
				Code:        "",
				Description: err.Error(),
			},
		}, nil
	}
	if name == "" {
		status = model.StatusDisabled
	} else {
		status = model.StatusEnabled
	}

	controllers, err := model.GetControllersInfo(r.Config.KubeClient, r.BrokerConn, r.meshsyncLivenessChannel)
	if err != nil {
		r.Log.Error(err)
		return &model.OperatorStatus{
			Status: status,
			Error: &model.Error{
				Code:        "",
				Description: err.Error(),
			},
		}, nil
	}

	return &model.OperatorStatus{
		Status:      status,
		Version:     version,
		Controllers: controllers,
	}, nil
}

func (r *Resolver) listenToOperatorState(ctx context.Context, provider models.Provider) (<-chan *model.OperatorStatus, error) {
	operatorChannel := make(chan *model.OperatorStatus)

	if r.operatorSyncChannel == nil {
		r.operatorSyncChannel = make(chan bool)
	}
	if r.meshsyncLivenessChannel == nil {
		r.meshsyncLivenessChannel = make(chan struct{})
	}

	operatorSyncChannel := make(chan broadcast.BroadcastMessage)
	r.Broadcast.Register(operatorSyncChannel)

	go func() {
		r.Log.Info("Operator subscription started")
		err := r.connectToBroker(context.TODO(), provider)
		if err != nil && err != ErrNoMeshSync {
			r.Log.Error(err)
			// The subscription should remain live to send future messages and only die when context is done
			// return
		}

		// Enforce enable operator
		status, err := r.getOperatorStatus(ctx, provider)
		if err != nil {
			r.Log.Error(ErrOperatorSubscription(err))
			// return
		}
		if status.Status != model.StatusEnabled {
			_, err = r.changeOperatorStatus(ctx, provider, model.StatusEnabled)
			if err != nil {
				r.Log.Error(ErrOperatorSubscription(err))
				// return
			}
		}
		for {
			select {
			case processing := <-operatorSyncChannel:
				r.Log.Info("Operator sync channel called")
				status, err := r.getOperatorStatus(ctx, provider)
				if err != nil {
					r.Log.Error(ErrOperatorSubscription(err))
					r.Log.Info("Operator subscription flushed")
					close(operatorChannel)
					// return
					continue
				}

				if processing.Message.(bool) {
					status.Status = model.StatusProcessing
				}

				operatorChannel <- status
			case <-ctx.Done():
				r.Log.Info("Operator subscription flushed")
				close(operatorChannel)
				r.Broadcast.Unregister(operatorSyncChannel)
				close(operatorSyncChannel)
				return
			}
		}
	}()

	return operatorChannel, nil
}
