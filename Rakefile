# frozen_string_literal: true

require "bundler/gem_tasks"

PLATFORMS = {
  "arm64-darwin"  => { goos: "darwin",  goarch: "arm64" },
  "x86_64-darwin" => { goos: "darwin",  goarch: "amd64" },
  "aarch64-linux" => { goos: "linux",   goarch: "arm64" },
  "x86_64-linux"  => { goos: "linux",   goarch: "amd64" },
}.freeze

GO_SRC = "ext/finesse"

require "rake/testtask"

namespace :test do
  desc "Run Go tests"
  task :go do
    Dir.chdir(GO_SRC) do
      sh "go test -v -count=1 ./..."
    end
  end

  Rake::TestTask.new(:ruby) do |t|
    t.libs << "test"
    t.test_files = FileList["test/**/*_test.rb"]
  end
end

desc "Run all tests"
task test: ["test:go", "test:ruby"]

task default: :test

namespace :build do
  desc "Cross-compile Go binary for a specific platform (e.g., rake build:go[arm64-darwin])"
  task :go, [:platform] do |_t, args|
    platform = args[:platform]
    unless PLATFORMS.key?(platform)
      abort "Unknown platform: #{platform}. Valid: #{PLATFORMS.keys.join(", ")}"
    end

    target = PLATFORMS[platform]
    out_dir = File.expand_path(File.join("exe", platform))
    mkdir_p out_dir

    env = {
      "CGO_ENABLED" => "0",
      "GOOS"        => target[:goos],
      "GOARCH"      => target[:goarch],
    }
    out = File.join(out_dir, "finesse")

    puts "Building #{platform} -> exe/#{platform}/finesse"
    Dir.chdir(GO_SRC) do
      env.each { |k, v| ENV[k] = v }
      sh "go build -trimpath -ldflags='-s -w' -o #{out} ."
    end
  end

  desc "Compile Go binary for the current platform"
  task :local do
    require_relative "lib/finesse/platforms"
    Rake::Task["build:go"].invoke(Finesse::Platforms.platform)
  end

  desc "Cross-compile Go binary for all platforms"
  task :all do
    PLATFORMS.each_key do |platform|
      Rake::Task["build:go"].reenable
      Rake::Task["build:go"].invoke(platform)
    end
  end
end

# Ensure Go binaries are compiled before gem packaging.
# `build` and `release` come from bundler/gem_tasks.
task build: "build:all"
