pipeline {
    agent {
        docker { 
            image 'golang:1.23'
            // Кешируем модули Go, чтобы не качать их каждый раз
            args '-v go-pkg-mod:/go/pkg/mod'
        }
    }
    
    environment {
        // Отключаем CGO для переносимости, если это возможно
        CGO_ENABLED = '0'
    }

    stages {
        stage('Check Env') {
            steps {
                sh 'go version'
                sh 'ls -la'
            }
        }

        stage('Build Agent') {
            steps {
                echo 'Building Agent binary...'
                // Агент собираем строго под Linux AMD64
                sh 'GOOS=linux GOARCH=amd64 go build -o elements/agent-install/agent cmd/agent/main.go'
            }
        }

        stage('Build Server') {
            steps {
                echo 'Building Image Manager Server...'
                sh 'go build -o image-manager cmd/image-manager/main.go'
            }
        }
    }

    post {
        always {
            // Очистка, если нужно
            cleanWs()
        }
        success {
            // Сохраняем собранные файлы как артефакты сборки
            archiveArtifacts artifacts: 'elements/agent-install/agent, image-manager', fingerprint: true
            echo 'Build success! Artifacts archived.'
        }
        failure {
            echo 'Build failed. Fix it!'
        }
    }
}
