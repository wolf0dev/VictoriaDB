.PHONY: all ui backend run dev clean

all: ui backend

ui:
	cd ui && npm install && npm run build

backend:
	go build -o victoriadb.exe .

run: all
	./victoriadb.exe --port 8090

dev-ui:
	cd ui && npm run dev

dev-backend:
	go run . --port 8090

clean:
	rm -rf ui/dist ui/node_modules victoriadb.exe victoria_data
