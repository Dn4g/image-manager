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
            image: bitnami/kubectl:latest
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
          
          sh '/kaniko/executor --context `pwd` --destination docker-registry.default.svc.cluster.local:5000/image-manager:latest --insecure --skip-tls-verify'
        }
      }
    }
    
    stage('Deploy to K8s') {
      steps {
        container('kubectl') {
          // Обновляем Deployment, чтобы он подтянул новый образ (imagePullPolicy: Always)
          sh 'kubectl rollout restart deployment/image-manager -n default'
        }
      }
    }
  }
}