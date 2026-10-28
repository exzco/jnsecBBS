git pull --ff-only origin main


cp config.example.yaml config.yaml
cp .env.example .env
cp ./deploy/nginx.conf.example ./deploy/nginx.conf

docker-compose build app
docker-compose up -d --no-deps app

docker ps -a --filter name=bbs-app


cp deploy/nginx.conf.example deploy/nginx.conf
if ! diff -q deploy/nginx.conf /etc/nginx/sites-available/bbs.jnsec.org >/dev/null; then
  sudo cp deploy/nginx.conf /etc/nginx/sites-available/bbs.jnsec.org
  sudo nginx -t && sudo nginx -s reload
fi
docker image prune -f