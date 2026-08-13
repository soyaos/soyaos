package process

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildRemotionExecuteRequest_MinimalArgv(t *testing.T) {
	req, err := BuildRemotionExecuteRequest(RemotionRenderSpec{
		EntryPoint:    "/workdir/src/index.ts",
		CompositionID: "MyClip",
		OutputPath:    "/workdir/out/clip.mp4",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := strings.Join(req.Cmd, " ")
	want := "npx remotion render /workdir/src/index.ts MyClip /workdir/out/clip.mp4"
	if got != want {
		t.Errorf("argv = %q\nwant %q", got, want)
	}
	if !reflect.DeepEqual(req.Access.FSRead, []string{"/workdir/src/index.ts"}) {
		t.Errorf("FSRead = %v, want entry point", req.Access.FSRead)
	}
	if !reflect.DeepEqual(req.Access.FSWrite, []string{"/workdir/out/clip.mp4"}) {
		t.Errorf("FSWrite = %v, want output path", req.Access.FSWrite)
	}
}

func TestBuildRemotionExecuteRequest_AllOptionalFlags(t *testing.T) {
	req, err := BuildRemotionExecuteRequest(RemotionRenderSpec{
		EntryPoint:    "/workdir/src/index.ts",
		CompositionID: "MyClip",
		OutputPath:    "/workdir/out/clip.mp4",
		PropsPath:     "/workdir/props.json",
		Concurrency:   4,
		FrameRange:    "0-300",
		Quality:       85,
		NPXBinary:     "/usr/local/bin/npx",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := strings.Join(req.Cmd, " ")
	want := "/usr/local/bin/npx remotion render /workdir/src/index.ts MyClip /workdir/out/clip.mp4 --props=/workdir/props.json --concurrency=4 --frames=0-300 --quality=85"
	if got != want {
		t.Errorf("argv = %q\nwant %q", got, want)
	}
	if !reflect.DeepEqual(req.Access.FSRead, []string{"/workdir/src/index.ts", "/workdir/props.json"}) {
		t.Errorf("FSRead = %v, want entry point + props", req.Access.FSRead)
	}
}

func TestBuildRemotionExecuteRequest_RejectsRelativePaths(t *testing.T) {
	cases := []RemotionRenderSpec{
		{EntryPoint: "src/index.ts", CompositionID: "C", OutputPath: "/workdir/clip.mp4"},
		{EntryPoint: "/workdir/src/index.ts", CompositionID: "C", OutputPath: "out/clip.mp4"},
		{EntryPoint: "/workdir/src/index.ts", CompositionID: "C", OutputPath: "/workdir/clip.mp4", PropsPath: "props.json"},
	}
	for i, spec := range cases {
		_, err := BuildRemotionExecuteRequest(spec)
		if err == nil {
			t.Errorf("case %d: expected error for relative path, got nil", i)
		}
	}
}

func TestBuildRemotionExecuteRequest_RejectsMissingRequired(t *testing.T) {
	cases := []RemotionRenderSpec{
		{},
		{EntryPoint: "/a/b.ts"},
		{EntryPoint: "/a/b.ts", CompositionID: "C"},
	}
	for i, spec := range cases {
		_, err := BuildRemotionExecuteRequest(spec)
		if err == nil {
			t.Errorf("case %d: expected error for missing required field, got nil", i)
		}
	}
}
