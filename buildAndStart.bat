cd D:/GoLand/rah/internal/studio/ui
npm run build
cd ../../..
docker compose build gateway
docker compose build studio
docker compose up -d --build
docker compose --profile studio up -d --build
