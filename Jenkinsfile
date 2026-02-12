pipeline {
  agent {
    kubernetes {
      yaml '''
        apiVersion: v1
        kind: Pod
        metadata:
          labels:
            some-label: some-label-value
        spec:
          containers:
          - name: kaniko
            image: gcr.io/kaniko-project/executor:debug
            command:
            - /busybox/cat
            tty: true
          - name: kubectl
            image: dtzar/helm-kubectl
            command:
            - cat
            tty: true
      '''
    }
  }
  stages {
    stage('Build with Kaniko') {
      steps {
        container('kaniko') {
          // Kaniko не использует Dockerfile из корня контекста по умолчанию, если он не там.
          // Используем внутреннее имя сервиса Registry: docker-registry.default.svc.cluster.local:5000
          // Это работает стабильнее внутри кластера.
          
          sh '/kaniko/executor --context `pwd` --destination 10.43.250.43:5000/image-manager:latest --cache=false --cache-repo=10.43.250.43:5000/image-manager-cache --insecure --skip-tls-verify'
        }
      }
    }
    
    stage('Deploy to K8s') {
      steps {
        container('kubectl') {
          // Принудительно обновляем образ на тот, что мы только что собрали во внутреннем реестре
          sh 'kubectl set image deployment/image-manager image-manager=10.43.250.43:5000/image-manager:latest -n default'
          // Перезапускаем, чтобы подтянуть изменения (особенно если тег latest не менялся, но хеш изменился)
          sh 'kubectl rollout restart deployment/image-manager -n default'
        }
      }
    }
  }
}
