package jobsched

import (
	"context"
	"fmt"
	"sync"
)

type ResponseWithError[T any] struct {
	response *T
	err      error
}

type ExecutorRunRequestWithId struct {
	Id int64
	*ExecutorRunRequest
}

type NewExecutor struct {
	Id           int64
	ResponseChan chan *ResponseWithError[ExecutorRunResponse]
}

type Server struct {
	UnimplementedJobSchedServer

	AddJobRequestChan chan *RequestWithChan[AddJobRequest, AddJobResponse]

	ExecutorIdChan        chan int64
	ExecutorRequestChan   chan *ExecutorRunRequestWithId
	NewExecutorChan       chan *NewExecutor
	RemoveExectorChan     chan int64
	ExecutorResponseChans map[int64]chan *ResponseWithError[ExecutorRunResponse]
}

func NewServer() *Server {
	return &Server{
		AddJobRequestChan:     make(chan *RequestWithChan[AddJobRequest, AddJobResponse]),
		ExecutorRequestChan:   make(chan *ExecutorRunRequestWithId),
		NewExecutorChan:       make(chan *NewExecutor),
		RemoveExectorChan:     make(chan int64),
		ExecutorResponseChans: make(map[int64]chan *ResponseWithError[ExecutorRunResponse]),
	}
}

func (j *Server) MainLoop(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()

	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()
		j.ExecutorIdChan = make(chan int64)

		defer func() {
			close(j.ExecutorIdChan)
		}()

		var id int64 = 0
	idloop:
		for {
			select {
			case <-ctx.Done():
				break idloop
			case j.ExecutorIdChan <- id:
			}

			id++
		}
	}(ctx)

mainloop:
	for {
		select {
		case <-ctx.Done():
			break mainloop
		case addJob := <-j.AddJobRequestChan:
			select {
			// need to make sure this response is sent back
			// case <-ctx.Done():
			case addJob.responseChan <- &ResponseWithError[AddJobResponse]{response: &AddJobResponse{}, err: nil}:
				close(addJob.responseChan)
			}

		case newExecutor := <-j.NewExecutorChan:
			j.ExecutorResponseChans[newExecutor.Id] = newExecutor.ResponseChan
		case removeId := <-j.RemoveExectorChan:
			removeChan, ok := j.ExecutorResponseChans[removeId]
			if ok {
				close(removeChan)
			}
			delete(j.ExecutorResponseChans, removeId)
		case executorRun := <-j.ExecutorRequestChan:
			//.. do stuff here
			_ = executorRun
		}
	}
}

var _ JobSchedServer = (*Server)(nil)

func (s *Server) ExecutorRun(bis JobSched_ExecutorRunServer) error {
	id, ok := <-s.ExecutorIdChan
	if !ok {
		return fmt.Errorf("main loop is not ready/running")
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(1)

	requestChan := make(chan *ExecutorRunRequest)

	go func() {
		defer wg.Done()
		defer func() {
			close(requestChan)
		}()
	recvloop:
		for {
			request, err := bis.Recv()
			if err != nil {
				break recvloop
			}

			requestChan <- request
		}
	}()

	respChan := make(chan *ResponseWithError[ExecutorRunResponse])
	s.NewExecutorChan <- &NewExecutor{Id: id, ResponseChan: respChan}
	defer func() {
		s.RemoveExectorChan <- id
	}()

executorloop:
	for {
		// note, either of those channel closing doesn't indicate the job is done.
		// there may still be jobs or responses from other end.
		// need to verify the logic works
		select {
		case request, ok := <-requestChan:
			if !ok {
				break executorloop // see note above
			}
			select {
			case s.ExecutorRequestChan <- &ExecutorRunRequestWithId{Id: id, ExecutorRunRequest: request}:
			}
		case response, ok := <-respChan:
			if !ok {
				break executorloop // see note above
			}
			if response.err != nil {
				return response.err // should not return, need fix
			}
			bis.Send(response.response)
		}
	}
	return nil
}

type RequestWithChan[TRequest any, TResponse any] struct {
	request      *TRequest
	responseChan chan<- *ResponseWithError[TResponse]
}

func (s *Server) AddJob(ctx context.Context, request *AddJobRequest) (*AddJobResponse, error) {
	respChan := make(chan *ResponseWithError[AddJobResponse])
	withChan := &RequestWithChan[AddJobRequest, AddJobResponse]{
		request:      request,
		responseChan: respChan,
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("cancelled")
	case s.AddJobRequestChan <- withChan:
	}

	select {
	// we don't check for done signal here because we need the mainloop to be able to send
	// case <-ctx.Done():
	// 	return nil, fmt.Errorf("cancelled")
	case resp, ok := <-respChan:
		if !ok {
			return nil, fmt.Errorf("cancelled")
		}
		return resp.response, resp.err
	}
}
