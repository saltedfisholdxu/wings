package docker

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/buger/jsonparser"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/pterodactyl/wings/config"
)

// PullImage pulls img into the local Docker engine. A configured proxy fetches
// the image in this process and loads it into Docker. Otherwise Docker pulls
// it, which is what applies the daemon's registry mirrors.
func PullImage(ctx context.Context, cli *client.Client, img string, onStatus func(string)) error {
	if strings.HasPrefix(img, "~") {
		return nil
	}

	if proxy, ok := config.Get().Docker.Proxy.ProxyURLForImage(img); ok {
		log.WithFields(log.Fields{"image": img, "proxy": config.MaskProxy(proxy)}).Info("pulling docker image through proxy")
		if onStatus != nil {
			onStatus("Pulling " + img + " through proxy")
		}
		if err := pullThroughProxy(ctx, cli, img, proxy); err != nil {
			log.WithFields(log.Fields{"image": img, "error": err}).Warn("proxy image pull failed, falling back to the docker daemon")
		} else {
			if onStatus != nil {
				onStatus("Downloaded " + img)
			}
			return nil
		}
	}

	return pullWithDaemon(ctx, cli, img, onStatus)
}

func pullThroughProxy(ctx context.Context, cli *client.Client, img, proxyURL string) error {
	proxy, err := url.Parse(proxyURL)
	if err != nil {
		return errors.Wrap(err, "parse image proxy")
	}
	ref, err := name.ParseReference(img)
	if err != nil {
		return errors.Wrap(err, "parse image reference")
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.Proxy = http.ProxyURL(proxy)
	defer transport.CloseIdleConnections()

	remoteImg, err := remote.Image(ref, remote.WithContext(ctx), remote.WithTransport(transport), remote.WithAuth(imageAuth(img)), remote.WithPlatform(v1.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}))
	if err != nil {
		return errors.Wrap(err, "download image through proxy")
	}

	dir := config.Get().System.TmpDirectory
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "wings-image-*.tar")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	if err := tarball.Write(ref, remoteImg, tmp); err != nil {
		return errors.Wrap(err, "write image archive")
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	resp, err := cli.ImageLoad(ctx, tmp, client.ImageLoadWithQuiet(true))
	if err != nil {
		return errors.Wrap(err, "load image into docker")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if msg, _ := jsonparser.GetString(body, "error"); msg != "" {
		return errors.New("docker load: " + msg)
	}
	return nil
}

func imageAuth(img string) authn.Authenticator {
	_, cred := config.Get().Docker.RegistryCredentialsForImage(img)
	if cred == nil || cred.Username == "" {
		return authn.Anonymous
	}
	return &authn.Basic{Username: cred.Username, Password: cred.Password}
}

func pullWithDaemon(ctx context.Context, cli *client.Client, img string, onStatus func(string)) error {
	registry, registryAuth := config.Get().Docker.RegistryCredentialsForImage(img)
	if registryAuth != nil {
		log.WithField("registry", registry).Debug("using authentication for registry")
	}

	imagePullOptions := image.PullOptions{All: false}
	if registryAuth != nil {
		b64, err := registryAuth.Base64()
		if err != nil {
			log.WithError(err).Error("failed to get registry auth credentials")
		}
		imagePullOptions.RegistryAuth = b64
	}

	out, err := cli.ImagePull(ctx, img, imagePullOptions)
	if err != nil {
		images, ierr := cli.ImageList(ctx, image.ListOptions{})
		if ierr != nil {
			return errors.Wrap(ierr, "environment/docker: failed to list images")
		}
		for _, img2 := range images {
			for _, tag := range img2.RepoTags {
				if tag != img {
					continue
				}
				log.WithFields(log.Fields{
					"image": img,
					"err":   err.Error(),
				}).Warn("unable to pull requested image from remote source, however the image exists locally")
				return nil
			}
		}
		return errors.Wrapf(err, "environment/docker: failed to pull \"%s\" image", img)
	}
	defer out.Close()

	log.WithField("image", img).Debug("pulling docker image... this could take a bit of time")
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		b := scanner.Bytes()
		status, _ := jsonparser.GetString(b, "status")
		progress, _ := jsonparser.GetString(b, "progress")
		line := strings.TrimSpace(status + " " + progress)
		if onStatus != nil && line != "" {
			onStatus(line)
		} else {
			log.Debug(scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	log.WithField("image", img).Debug("completed docker image pull")
	return nil
}
