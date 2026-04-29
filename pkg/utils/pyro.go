package utils

import (
	"runtime"

	"github.com/grafana/pyroscope-go"
)

func StartPyroScope() error {
	pyroscopeUrl := GetEnv("PYROSCOPE_URL")
	if pyroscopeUrl == "" {
		return nil
	}

	pyroscopeAppName := GetEnv("PYROSCOPE_APP_NAME")
	if pyroscopeAppName == "" {
		pyroscopeAppName = "ling-voice"
	}
	pyroscopeBasicAuthUser := GetEnv("PYROSCOPE_BASIC_AUTH_USER")
	pyroscopeBasicAuthPassword := GetEnv("PYROSCOPE_BASIC_AUTH_PASSWORD")
	pyroscopeHostname := GetEnv("HOSTNAME")
	if pyroscopeHostname == "" {
		pyroscopeHostname = "ling-voice"
	}

	mutexRate := GetIntEnv("PYROSCOPE_MUTEX_RATE")
	if mutexRate == 0 {
		mutexRate = 5
	}
	blockRate := GetIntEnv("PYROSCOPE_BLOCK_RATE")
	if blockRate == 0 {
		blockRate = 5
	}

	runtime.SetMutexProfileFraction(int(mutexRate))
	runtime.SetBlockProfileRate(int(blockRate))

	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: pyroscopeAppName,

		ServerAddress:     pyroscopeUrl,
		BasicAuthUser:     pyroscopeBasicAuthUser,
		BasicAuthPassword: pyroscopeBasicAuthPassword,

		Logger: nil,

		Tags: map[string]string{"hostname": pyroscopeHostname},

		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
	if err != nil {
		return err
	}
	return nil
}
