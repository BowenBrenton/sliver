package rpc

/*
	Sliver Implant Framework
	Copyright (C) 2019  Bishop Fox

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU General Public License for more details.

	You should have received a copy of the GNU General Public License
	along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"

	consts "github.com/bishopfox/sliver/client/constants"
	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/server/codenames"
	"github.com/bishopfox/sliver/server/core"
	"github.com/bishopfox/sliver/server/cryptography"
	"github.com/bishopfox/sliver/server/db"
	"github.com/bishopfox/sliver/server/generate"
	"github.com/bishopfox/sliver/server/log"
	"github.com/bishopfox/sliver/util"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	rcpLog = log.NamedLogger("rpc", "generate")
)

// Generate - Generate a new implant
func (rpc *Server) Generate(ctx context.Context, req *clientpb.GenerateReq) (*clientpb.Generate, error) {
	var fPath string
	var err error
	name, config := generate.ImplantConfigFromProtobuf(req.Config)
	if name == "" {
		name, err = codenames.GetCodename()
		if err != nil {
			return nil, err
		}
	}

	if config == nil {
		return nil, errors.New("invalid implant config")
	}
	switch req.Config.Format {
	case clientpb.OutputFormat_SERVICE:
		fallthrough
	case clientpb.OutputFormat_EXECUTABLE:
		fPath, err = generate.SliverExecutable(name, config)
	case clientpb.OutputFormat_SHARED_LIB:
		fPath, err = generate.SliverSharedLibrary(name, config)
	case clientpb.OutputFormat_SHELLCODE:
		fPath, err = generate.SliverShellcode(name, config)
	default:
		return nil, fmt.Errorf("invalid output format: %s", req.Config.Format)
	}

	if err != nil {
		return nil, err
	}

	filename := filepath.Base(fPath)
	fileData, err := ioutil.ReadFile(fPath)
	if err != nil {
		return nil, err
	}

	core.EventBroker.Publish(core.Event{
		EventType: consts.BuildCompletedEvent,
		Data:      []byte(fmt.Sprintf("%s build completed", filename)),
	})

	return &clientpb.Generate{
		File: &commonpb.File{
			Name: filename,
			Data: fileData,
		},
	}, err
}

// Regenerate - Regenerate a previously generated implant
func (rpc *Server) Regenerate(ctx context.Context, req *clientpb.RegenerateReq) (*clientpb.Generate, error) {

	build, err := db.ImplantBuildByName(req.ImplantName)
	if err != nil {
		return nil, err
	}

	fileData, err := generate.ImplantFileFromBuild(build)
	if err != nil {
		return nil, err
	}

	return &clientpb.Generate{
		File: &commonpb.File{
			Name: build.ImplantConfig.FileName,
			Data: fileData,
		},
	}, nil
}

// ImplantBuilds - List existing implant builds
func (rpc *Server) ImplantBuilds(ctx context.Context, _ *commonpb.Empty) (*clientpb.ImplantBuilds, error) {
	dbBuilds, err := db.ImplantBuilds()
	if err != nil {
		return nil, err
	}
	pbBuilds := &clientpb.ImplantBuilds{
		Configs: map[string]*clientpb.ImplantConfig{},
	}
	for _, dbBuild := range dbBuilds {
		pbBuilds.Configs[dbBuild.Name] = dbBuild.ImplantConfig.ToProtobuf()
	}
	return pbBuilds, nil
}

// Canaries - List existing canaries
func (rpc *Server) Canaries(ctx context.Context, _ *commonpb.Empty) (*clientpb.Canaries, error) {
	dbCanaries, err := db.ListCanaries()
	if err != nil {
		return nil, err
	}

	rpcLog.Infof("Found %d canaries", len(dbCanaries))
	canaries := []*clientpb.DNSCanary{}
	for _, canary := range dbCanaries {
		canaries = append(canaries, canary.ToProtobuf())
	}

	return &clientpb.Canaries{
		Canaries: canaries,
	}, nil
}

// GenerateUniqueIP - Wrapper around generate.GenerateUniqueIP
func (rpc *Server) GenerateUniqueIP(ctx context.Context, _ *commonpb.Empty) (*clientpb.UniqueWGIP, error) {
	uniqueIP, err := generate.GenerateUniqueIP()

	if err != nil {
		rpcLog.Infof("Failed to generate unique wg peer ip: %s\n", err)
		return nil, err
	}

	return &clientpb.UniqueWGIP{
		IP: uniqueIP.String(),
	}, nil
}

// ImplantProfiles - List profiles
func (rpc *Server) ImplantProfiles(ctx context.Context, _ *commonpb.Empty) (*clientpb.ImplantProfiles, error) {
	implantProfiles := &clientpb.ImplantProfiles{
		Profiles: []*clientpb.ImplantProfile{},
	}
	dbProfiles, err := db.ImplantProfiles()
	if err != nil {
		return implantProfiles, err
	}
	for _, dbProfile := range dbProfiles {
		implantProfiles.Profiles = append(implantProfiles.Profiles, &clientpb.ImplantProfile{
			Name:   dbProfile.Name,
			Config: dbProfile.ImplantConfig.ToProtobuf(),
		})
	}
	return implantProfiles, nil
}

// SaveImplantProfile - Save a new profile
func (rpc *Server) SaveImplantProfile(ctx context.Context, profile *clientpb.ImplantProfile) (*clientpb.ImplantProfile, error) {
	_, config := generate.ImplantConfigFromProtobuf(profile.Config)
	profile.Name = filepath.Base(profile.Name)
	if 0 < len(profile.Name) && profile.Name != "." {
		rpcLog.Infof("Saving new profile with name %#v", profile.Name)
		err := generate.SaveImplantProfile(profile.Name, config)
		if err != nil {
			return nil, err
		}
		core.EventBroker.Publish(core.Event{
			EventType: consts.ProfileEvent,
			Data:      []byte(profile.Name),
		})
		return profile, nil
	}
	return nil, errors.New("invalid profile name")
}

// DeleteImplantProfile - Delete an implant profile
func (rpc *Server) DeleteImplantProfile(ctx context.Context, req *clientpb.DeleteReq) (*commonpb.Empty, error) {
	profile, err := db.ProfileByName(req.Name)
	if err != nil {
		return nil, err
	}
	err = db.Session().Delete(profile).Error
	if err == nil {
		core.EventBroker.Publish(core.Event{
			EventType: consts.ProfileEvent,
			Data:      []byte(profile.Name),
		})
	}
	return &commonpb.Empty{}, err
}

// DeleteImplantBuild - Delete an implant build
func (rpc *Server) DeleteImplantBuild(ctx context.Context, req *clientpb.DeleteReq) (*commonpb.Empty, error) {
	build, err := db.ImplantBuildByName(req.Name)
	if err != nil {
		return nil, err
	}
	err = db.Session().Delete(build).Error
	if err != nil {
		return nil, err
	}
	err = generate.ImplantFileDelete(build)
	if err == nil {
		core.EventBroker.Publish(core.Event{
			EventType: consts.BuildEvent,
			Data:      []byte(build.Name),
		})
	}
	return &commonpb.Empty{}, err
}

// ShellcodeRDI - Generates a RDI shellcode from a given DLL
func (rpc *Server) ShellcodeRDI(ctx context.Context, req *clientpb.ShellcodeRDIReq) (*clientpb.ShellcodeRDI, error) {
	shellcode, err := generate.ShellcodeRDIFromBytes(req.GetData(), req.GetFunctionName(), req.GetArguments())
	return &clientpb.ShellcodeRDI{Data: shellcode}, err
}

// GetCompiler - Get information about the internal Go compiler and its configuration
func (rpc *Server) GetCompiler(ctx context.Context, _ *commonpb.Empty) (*clientpb.Compiler, error) {
	compiler := &clientpb.Compiler{
		GOOS:               runtime.GOOS,
		GOARCH:             runtime.GOARCH,
		Targets:            generate.GetCompilerTargets(),
		UnsupportedTargets: generate.GetUnsupportedTargets(),
		CrossCompilers:     generate.GetCrossCompilers(),
	}
	rcpLog.Debugf("GetCompiler = %v", compiler)
	return compiler, nil
}

// *** External builder RPCs ***

// Generate - Generate a new implant
func (rpc *Server) GenerateExternal(ctx context.Context, req *clientpb.GenerateReq) (*clientpb.ExternalImplantConfig, error) {
	var err error
	name, config := generate.ImplantConfigFromProtobuf(req.Config)
	if name == "" {
		name, err = codenames.GetCodename()
		if err != nil {
			return nil, err
		}
	}
	if config == nil {
		return nil, errors.New("invalid implant config")
	}
	req.Config.Format = clientpb.OutputFormat_EXTERNAL
	externalConfig, err := generate.SliverExternal(name, config)
	if err != nil {
		return nil, err
	}

	core.EventBroker.Publish(core.Event{
		EventType: consts.ExternalBuildEvent,
		Data:      []byte(config.ID.String()),
	})

	return externalConfig, err
}

func (rpc *Server) GenerateExternalSaveBuild(ctx context.Context, req *clientpb.ExternalImplantBinary) (*commonpb.Empty, error) {
	implantConfig, err := db.ImplantConfigByID(req.ImplantConfigID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid implant config id")
	}
	if implantConfig.Format != clientpb.OutputFormat_EXTERNAL {
		return nil, status.Error(codes.InvalidArgument, "invalid implant config format")
	}
	err = util.AllowedName(req.Name)
	if err != nil {
		rpcLog.Errorf("Invalid build name: %s", err)
		return nil, ErrInvalidName
	}
	_, err = db.ImplantBuildByName(req.Name)
	if err == nil {
		return nil, ErrBuildExists
	}

	tmpFile, err := ioutil.TempFile("", "sliver-external-build")
	if err != nil {
		rpcLog.Errorf("Failed to create temporary file: %s", err)
		return nil, status.Error(codes.Internal, "Failed to write implant binary to temp file")
	}
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.Write(req.File.Data)
	if err != nil {
		rcpLog.Errorf("Failed to write implant binary to temp file: %s", err)
		return nil, status.Error(codes.Internal, "Failed to write implant binary to temp file")
	}
	err = generate.ImplantBuildSave(req.Name, implantConfig, "")
	if err != nil {
		return nil, err
	}

	core.EventBroker.Publish(core.Event{
		EventType: consts.BuildCompletedEvent,
		Data:      []byte(fmt.Sprintf("%s build completed", req.Name)),
	})

	return &commonpb.Empty{}, nil
}

func (rpc *Server) GenerateExternalGetImplantConfig(ctx context.Context, req *clientpb.ImplantConfig) (*clientpb.ExternalImplantConfig, error) {
	implantConfig, err := db.ImplantConfigByID(req.ID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid implant config id")
	}
	if implantConfig.Format != clientpb.OutputFormat_EXTERNAL {
		return nil, status.Error(codes.InvalidArgument, "invalid implant config format")
	}

	name, err := codenames.GetCodename()
	if err != nil {
		return nil, err
	}

	otpSecret, err := cryptography.TOTPServerSecret()
	if err != nil {
		return nil, err
	}

	return &clientpb.ExternalImplantConfig{
		Name:      name,
		Config:    implantConfig.ToProtobuf(),
		OTPSecret: otpSecret,
	}, nil
}
