package kubernetestracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dgruber/drmaa2interface"
	"github.com/dgruber/drmaa2os"
	"github.com/dgruber/drmaa2os/pkg/helper"
	"github.com/dgruber/drmaa2os/pkg/jobtracker"
	k8sapi "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const K8S_JT_EXTENSION_NAMESPACE = "namespace"
const K8S_JT_EXTENSION_LABELS = "labels"

type KubernetesTracker struct {
	clientSet  *kubernetes.Clientset
	jobsession string
}

// init registers the Kubernetes job tracker at the SessionManager
func init() {
	drmaa2os.RegisterJobTracker(drmaa2os.KubernetesSession, NewAllocator())
}

type allocator struct{}

func NewAllocator() *allocator {
	return &allocator{}
}

// New is called by the SessionManager when a new JobSession is allocated.
// jobTrackerInitParams must be a kubernetes.Clientset if not nil. If nil
// a new Clientset is allocated.
func (a *allocator) New(jobSessionName string, jobTrackerInitParams interface{}) (jobtracker.JobTracker, error) {
	var cs *kubernetes.Clientset
	//
	if jobTrackerInitParams != nil {
		var ok bool
		cs, ok = jobTrackerInitParams.(*kubernetes.Clientset)
		if !ok {
			return nil, errors.New("jobTrackerInitParams is not of type *kubernetes.Clientset")
		}
	}
	return New(jobSessionName, cs)
}

// New creates a new KubernetesTracker either by using a given kubernetes Clientset
// or by allocating a new one (if the parameter is zero).
func New(jobsession string, cs *kubernetes.Clientset) (*KubernetesTracker, error) {
	if cs == nil {
		var err error
		cs, err = NewClientSet()
		if err != nil {
			return nil, err
		}
	}
	return &KubernetesTracker{
		clientSet:  cs,
		jobsession: jobsession,
	}, nil
}

func (kt *KubernetesTracker) ListJobCategories() ([]string, error) {
	return []string{}, nil
}

// ListJobs returns a list of job IDs associated with the current
// DRMAA2 job session.
func (kt *KubernetesTracker) ListJobs() ([]string, error) {
	jc, err := getJobsClient(kt.clientSet)
	if err != nil {
		return nil, fmt.Errorf("ListJobs: %s", err.Error())
	}
	labelSelector := fmt.Sprintf("drmaa2jobsession=%s", kt.jobsession)
	jobsList, err := jc.List(context.TODO(), k8sapi.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, fmt.Errorf("listing jobs with client: %s", err.Error())
	}
	ids := make([]string, 0, len(jobsList.Items))
	for _, job := range jobsList.Items {
		ids = append(ids, string(job.Name))
	}
	return ids, nil
}

// AddJob converts the given DRMAA2 job template into a batchv1.Job and creates
// the job within Kubernetes.
func (kt *KubernetesTracker) AddJob(jt drmaa2interface.JobTemplate) (string, error) {
	job, err := convertJob(kt.jobsession, jt)
	if err != nil {
		return "", fmt.Errorf("converting job template into a k8s job: %s", err.Error())
	}
	jc, err := getJobsClient(kt.clientSet)
	if err != nil {
		return "", fmt.Errorf("get client: %s", err.Error())
	}
	j, err := jc.Create(context.TODO(), job, k8sapi.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("creating new job: %s", err.Error())
	}
	return string(j.Name), nil
}

func (kt *KubernetesTracker) AddArrayJob(jt drmaa2interface.JobTemplate, begin int, end int, step int, maxParallel int) (string, error) {
	return helper.AddArrayJobAsSingleJobs(jt, kt, begin, end, step)
}

func (kt *KubernetesTracker) ListArrayJobs(id string) ([]string, error) {
	return helper.ArrayJobID2GUIDs(id)
}

func (kt *KubernetesTracker) JobState(jobid string) (drmaa2interface.JobState, string, error) {
	jc, err := getJobsClient(kt.clientSet)
	if err != nil {
		return drmaa2interface.Undetermined, "", nil
	}
	return DRMAA2State(jc, jobid), "", nil
}

func (kt *KubernetesTracker) JobInfo(jobid string) (drmaa2interface.JobInfo, error) {
	jc, err := getJobsClient(kt.clientSet)
	if err != nil {
		return drmaa2interface.JobInfo{}, err
	}
	return JobToJobInfo(jc, jobid)
}

// JobControl changes the state of the given job by execution the given action
// (suspend, resume, hold, release, terminate).
func (kt *KubernetesTracker) JobControl(jobid, state string) error {
	jc, job, err := getJobInterfaceAndJob(kt.clientSet, jobid)
	if err != nil {
		return fmt.Errorf("JobControl: %s", err.Error())
	}
	return jobStateChange(jc, job, state)
}

// Wait returns when the job is in one of the given states or when a timeout
// occurs (errors then).
func (kt *KubernetesTracker) Wait(jobid string, timeout time.Duration, states ...drmaa2interface.JobState) error {
	return helper.WaitForState(kt, jobid, timeout, states...)
}

// DeleteJob removes a job from kubernetes.
func (kt *KubernetesTracker) DeleteJob(jobid string) error {
	jc, job, err := getJobInterfaceAndJob(kt.clientSet, jobid)
	if err != nil {
		return fmt.Errorf("DeleteJob: %s", err.Error())
	}
	return deleteJob(jc, job)
}
