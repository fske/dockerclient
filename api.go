package dockerclient

import (
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"golang.org/x/net/context"
)

// DockerClient :
type DockerClient struct {
	cli               *client.Client
	forwardHubDomain  string
	authStrForwardHub string
	sourceHubDomain   string
	authStrSourceHub  string
}

// NewDockerClient :
func NewDockerClient(
	daemonHost string,
	daemonAPIVersion string,
	forwardHubDomain string,
	forwardHubUsername string,
	forwardHubPassword string,
	sourceHubDomain string,
	sourceHubUsername string,
	sourceHubPassword string,
) (
	dockerClient *DockerClient,
	err error,
) {
	var c = DockerClient{}
	c.forwardHubDomain = forwardHubDomain
	c.authStrForwardHub = genAuthStr(forwardHubUsername, forwardHubPassword)
	c.sourceHubDomain = sourceHubDomain
	c.authStrSourceHub = genAuthStr(sourceHubUsername, sourceHubPassword)

	tr := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    5 * 60 * time.Second,
		DisableCompression: true,
	}
	httpClient := &http.Client{Transport: tr}
	c.cli, err = client.NewClient("http://"+daemonHost, daemonAPIVersion, httpClient, nil)
	if err != nil {
		log.Println("fail to init docker: ", err)
		return
	}
	dockerClient = &c
	return
}

func genAuthStr(username string, password string) (authStr string) {
	authConfig := types.AuthConfig{
		Username: username,
		Password: password,
	}
	encodedJSON, _ := json.Marshal(authConfig)
	return base64.URLEncoding.EncodeToString(encodedJSON)
}

// TransferImageToForwardHub :
func (c *DockerClient) TransferImageToForwardHub(
	ctx context.Context,
	dockerProject string,
	imageName string,
) (
	forwardHubPath string,
	err error,
) {
	imageFullnameSourceHub := c.sourceHubDomain + "/" + dockerProject + "/" + imageName
	imageFullnameForwardHub := c.forwardHubDomain + "/" + dockerProject + "_" + imageName
	resp, err := c.cli.ImagePull(ctx, imageFullnameSourceHub, types.ImagePullOptions{
		All:          false,
		RegistryAuth: c.authStrSourceHub,
	})
	if err != nil {
		log.Println("fail to pull image: ", err)
		return
	}
	respBytes, err := ioutil.ReadAll(resp)
	if err != nil {
		log.Println("fail to read pull image resp: ", err)
		return
	}
	log.Println(string(respBytes[100]))
	resp.Close()

	err = c.cli.ImageTag(ctx, imageFullnameSourceHub, imageFullnameForwardHub)
	if err != nil {
		log.Println("fail to tag image: ", err)
		return
	}

	resp, err = c.cli.ImagePush(ctx, imageFullnameForwardHub, types.ImagePushOptions{
		RegistryAuth: c.authStrForwardHub,
	})
	if err != nil {
		log.Println("fail to push image: ", err)
		return
	}
	respBytes, err = ioutil.ReadAll(resp)
	if err != nil {
		log.Println("fail to read push image resp: ", err)
		return
	}
	log.Println(string(respBytes[100]))
	resp.Close()

	// remove image
	_, err = c.cli.ImageRemove(ctx, imageFullnameForwardHub, types.ImageRemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	if err != nil {
		log.Println("fail to remove forwardHub image: ", err)
		return
	}
	_, err = c.cli.ImageRemove(ctx, imageFullnameSourceHub, types.ImageRemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	if err != nil {
		log.Println("fail to remove sourceHub image: ", err)
		return
	}
	forwardHubPath = imageFullnameForwardHub
	return
}

// CreateContainer :
func (c *DockerClient) CreateContainer(
	ctx context.Context,
	image string,
	cmds []string,
) (
	resp container.ContainerCreateCreatedBody,
	err error,
) {
	resp, err = c.cli.ContainerCreate(ctx, &container.Config{
		Image: image,
		Cmd:   cmds,
	}, nil, nil, "")
	if err != nil {
		log.Fatal(err)
	}
	return
}

// StartContainer :
func (c *DockerClient) StartContainer(
	ctx context.Context,
	createResp container.ContainerCreateCreatedBody,
) (
	err error,
) {
	if err := c.cli.ContainerStart(ctx, createResp.ID, types.ContainerStartOptions{}); err != nil {
		log.Fatal(err)
	}

	statusCh, errCh := c.cli.ContainerWait(ctx, createResp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			log.Fatal(err)
		}
	case <-statusCh:
	}

	out, err := c.cli.ContainerLogs(ctx, createResp.ID, types.ContainerLogsOptions{ShowStdout: true})
	if err != nil {
		log.Fatal(err)
	}

	stdcopy.StdCopy(os.Stdout, os.Stderr, out)
	return
}
