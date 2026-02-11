# Инструкция по развертыванию (Kubernetes / K3s)

Вся инфраструктура (Image Manager, Jenkins, Registry, RabbitMQ) работает на одном сервере под управлением K3s.

## 1. Установка K3s

```bash
# Установка без Traefik (будем использовать Nginx Ingress)
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--disable traefik" sh -

# Настройка доступа к kubectl для текущего пользователя
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown $(id -u):$(id -g) ~/.kube/config
echo "export KUBECONFIG=~/.kube/config" >> ~/.bashrc
source ~/.bashrc
```

## 2. Настройка Ingress (Nginx)

```bash
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo update
helm install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx \
  --create-namespace \
  --set controller.service.type=LoadBalancer
```

## 3. Инфраструктура

### Docker Registry
Локальный реестр для образов.

1.  Применить манифест `k3s/registry.yaml` (Deployment + PVC + Ingress).
2.  Настроить Docker и K3s на использование insecure registry `registry.dn4g.ru` (см. `/etc/docker/daemon.json` и `/etc/rancher/k3s/registries.yaml`).
3.  Добавить домен в `/etc/hosts`.

### Jenkins
Устанавливается через Helm.

```bash
helm install jenkins jenkins/jenkins \
  --namespace jenkins \
  --create-namespace \
  --set controller.ingress.enabled=true \
  --set controller.ingress.hostName=jenkins.test.com \
  --set controller.ingress.ingressClassName=nginx \
  --set controller.serviceType=ClusterIP
```
Важно: Настроить "Jenkins Tunnel" в конфиге облака на `jenkins-agent.jenkins.svc.cluster.local:50000`.

### RabbitMQ
Устанавливается через Helm (Bitnami legacy) или манифест.

## 4. Деплой приложения

1.  Создать секреты в K8s (или отредактировать `k3s/image-manager.yaml`, но не коммитить секреты!).
2.  Применить манифест:
    ```bash
    kubectl apply -f k3s/image-manager.yaml
    ```
3.  Перезапустить под при обновлении конфига:
    ```bash
    kubectl rollout restart deployment image-manager
    ```

Доступ к вебу: `http://image-manager.yourdomain.ru`
Доступ для агентов (gRPC): `http://grpc.yourdomain.ru` (порт 80)

