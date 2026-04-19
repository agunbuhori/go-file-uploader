package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"big-file-service/api"
	"big-file-service/internal/config"
	applogger "big-file-service/internal/logger"
	"big-file-service/internal/s3client"
	"big-file-service/internal/uploader"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger, err := applogger.New(cfg.Log)
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = logger.Sync()
	}()

	ctx := context.Background()
	s3Client, err := s3client.New(ctx, cfg.AWS, cfg.S3)
	if err != nil {
		logger.Fatal("failed creating s3 client", zap.Error(err))
	}

	uploadService := uploader.NewService(s3Client, cfg.Upload, cfg.S3, logger)
	handler := api.NewHandler(uploadService, cfg, logger)

	server := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      handler.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		if cfg.Security.TLSEnabled {
			logger.Info("server started with TLS", zap.String("addr", server.Addr))
			serverErrors <- server.ListenAndServeTLS(cfg.Security.TLSCertPath, cfg.Security.TLSKeyPath)
			return
		}

		logger.Info("server started", zap.String("addr", server.Addr))
		serverErrors <- server.ListenAndServe()
	}()

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-signalChannel:
		logger.Info("shutdown signal received", zap.String("signal", sig.String()))
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server failed", zap.Error(err))
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown failed", zap.Error(err))
	}

	if err := uploadService.WaitForInFlight(shutdownCtx); err != nil {
		logger.Error("waiting for in-flight uploads failed", zap.Error(err))
	}

	logger.Info("server stopped")
}
