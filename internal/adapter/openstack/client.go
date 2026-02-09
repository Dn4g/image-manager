package openstack

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"

	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/keypairs"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/imagedata"
	"github.com/gophercloud/gophercloud/openstack/imageservice/v2/images"
)

// Client — структура клиента
type Client struct {
	log          *slog.Logger
	imagesClient *gophercloud.ServiceClient
	sshKeyName   string // <-- Исправлено: просто тип string
	region       string
}

// NewClient создает клиент.
func NewClient(log *slog.Logger, authUrl, username, password, projectID, projectName, domainName, region, sshKeyName string) (*Client, error) {
	opts := gophercloud.AuthOptions{
		IdentityEndpoint: authUrl,
		Username:         username,
		Password:         password,
		TenantID:         projectID,
		TenantName:       projectName,
		DomainName:       domainName,
		AllowReauth:      true,
	}

	provider, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		return nil, fmt.Errorf("auth failed: %w", err)
	}

	endpointOpts := gophercloud.EndpointOpts{
		Region: region,
	}

	imgClient, err := openstack.NewImageServiceV2(provider, endpointOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create image client: %w", err)
	}

	return &Client{
		log:          log,
		imagesClient: imgClient,
		sshKeyName:   sshKeyName, // <-- Значение присваивается здесь!
	}, nil
}

// ImageInfo — упрощенная структура для фронтенда
type ImageInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
}

// ListImages возвращает список образов из Glance.
func (c *Client) ListImages() ([]ImageInfo, error) {
	allPages, err := images.List(c.imagesClient, images.ListOpts{}).AllPages()
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	allImages, err := images.ExtractImages(allPages)
	if err != nil {
		return nil, fmt.Errorf("failed to extract images: %w", err)
	}

	var result []ImageInfo
	for _, img := range allImages {
		result = append(result, ImageInfo{
			ID:        img.ID,
			Name:      img.Name,
			Status:    string(img.Status),
			Size:      img.SizeBytes,
			CreatedAt: img.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return result, nil
}

// UploadImage загружает локальный файл в Glance.
func (c *Client) UploadImage(filePath string, imageName string) (string, error) {
	const op = "openstack.UploadImage"
	c.log.Info("starting image upload", slog.String("file", filePath), slog.String("name", imageName))

	visibility := images.ImageVisibilityPrivate

	createOpts := images.CreateOpts{
		Name:            imageName,
		ContainerFormat: "bare",
		DiskFormat:      "qcow2",
		Visibility:      &visibility,
		Properties: map[string]string{
			"hw_qemu_guest_agent": "yes",
			"os_distro":           "linux",
		},
	}

	img, err := images.Create(c.imagesClient, createOpts).Extract()
	if err != nil {
		return "", fmt.Errorf("%s: create metadata failed: %w", op, err)
	}
	c.log.Debug("image metadata created", slog.String("id", img.ID))

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("%s: open file failed: %w", op, err)
	}
	defer f.Close()

	res := imagedata.Upload(c.imagesClient, img.ID, f)
	if res.Err != nil {
		return "", fmt.Errorf("%s: upload data failed: %w", res.Err)
	}

	c.log.Info("image uploaded successfully", slog.String("id", img.ID))
	return img.ID, nil
}

// CreateVM создает сервер в OpenStack.
func (c *Client) CreateVM(name, imageID, flavorID, netID, userData string) (string, error) {
	const op = "openstack.CreateVM"

	computeClient, err := openstack.NewComputeV2(c.imagesClient.ProviderClient, gophercloud.EndpointOpts{
		Region: c.region,
	})
	if err != nil {
		return "", fmt.Errorf("%s: compute client error: %w", op, err)
	}

	createOpts := servers.CreateOpts{
		Name:      name,
		ImageRef:  imageID,
		FlavorRef: flavorID,
		UserData:  []byte(userData),
		Networks: []servers.Network{
			{UUID: netID},
		},
	}

	createOptsWithKey := keypairs.CreateOptsExt{
		CreateOptsBuilder: createOpts,
		KeyName:           c.sshKeyName, // Берем из поля структуры
	}

	server, err := servers.Create(computeClient, createOptsWithKey).Extract()
	if err != nil {
		return "", fmt.Errorf("%s: create failed: %w", op, err)
	}

	c.log.Info("vm created", slog.String("id", server.ID), slog.String("key", c.sshKeyName))
	return server.ID, nil
}

// WaitForVMActive ждет, пока VM перейдет в статус ACTIVE.
func (c *Client) WaitForVMActive(serverID string, timeout time.Duration) error {
	op := "openstack.WaitForVMActive"
	c.log.Info("waiting for vm to become active", slog.String("id", serverID))

	// Инициализация compute client (лучше вынести, но пока так)
	computeClient, err := openstack.NewComputeV2(c.imagesClient.ProviderClient, gophercloud.EndpointOpts{
		Region: c.region,
	})
	if err != nil {
		return fmt.Errorf("%s: compute client error: %w", op, err)
	}

	return servers.WaitForStatus(computeClient, serverID, "ACTIVE", int(timeout.Seconds()))
}

func (c *Client) DeleteVM(serverID string) error {
        const op = "openstack.DeleteVM"

    // Снова ленивая инициализация Compute (можно вынести в структуру Client, чтобы не создавать каждый раз)
        computeClient, err := openstack.NewComputeV2(c.imagesClient.ProviderClient, gophercloud.EndpointOpts{
        Region: c.region,
        })
        if err != nil {
            return fmt.Errorf("%s: compute client error: %w", op, err)
        }

        if err := servers.Delete(computeClient, serverID).ExtractErr(); err != nil {
            return fmt.Errorf("%s: delete failed: %w", op, err)
        }

        return nil
}

// DeleteImageByName удаляет ВСЕ образы с таким именем (если есть дубли).
func (c *Client) DeleteImageByName(name string) error {
    // Нам нужен ListOpts
        pages, err := images.List(c.imagesClient, images.ListOpts{Name: name}).AllPages()
        if err != nil {
        return err
    }
    allImages, err := images.ExtractImages(pages)
    if err != nil {
        return err
    }

    for _, img := range allImages {
        c.log.Info("deleting old image", slog.String("id", img.ID), slog.String("name", name))
        if err := images.Delete(c.imagesClient, img.ID).ExtractErr(); err != nil {
            c.log.Error("failed to delete old image", slog.String("err", err.Error()))
        }
    }
    return nil
}

