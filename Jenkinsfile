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

  environment {
    REGISTRY = 'docker-registry.default.svc.cluster.local:5000'
    IMAGE    = 'image-manager'
    TAG      = "${env.GIT_COMMIT?.take(8) ?: env.BUILD_NUMBER}"
  }

  stages {
    stage('Build with Kaniko') {
      steps {
        container('kaniko') {
          sh """/kaniko/executor \
            --context `pwd` \
            --destination ${REGISTRY}/${IMAGE}:${TAG} \
            --destination ${REGISTRY}/${IMAGE}:latest \
            --insecure \
            --skip-tls-verify"""
        }
      }
    }

    stage('Deploy to K8s') {
      steps {
        container('kubectl') {
          sh "kubectl set image deployment/image-manager image-manager=${REGISTRY}/${IMAGE}:${TAG} -n default"
          sh 'kubectl rollout status deployment/image-manager -n default --timeout=120s'
        }
      }
    }
  }
}
