# frozen_string_literal: true

require 'bundler/gem_tasks'

PLATFORMS = {
  'arm64-darwin' => { goos: 'darwin', goarch: 'arm64' },
  'x86_64-darwin' => { goos: 'darwin', goarch: 'amd64' },
  'aarch64-linux' => { goos: 'linux', goarch: 'arm64' },
  'x86_64-linux' => { goos: 'linux', goarch: 'amd64' }
}.freeze

GO_SRC = 'ext/finesse'

require 'rake/testtask'

namespace :test do
  desc 'Run Go tests'
  task :go do
    Dir.chdir(GO_SRC) { sh 'go test -v -count=1 ./...' }
  end

  Rake::TestTask.new(:ruby) do |t|
    t.libs << 'test'
    t.test_files = FileList['test/**/*_test.rb']
  end
end

require 'rubocop/rake_task'
RuboCop::RakeTask.new

desc 'Run all tests'
task test: %w[test:go test:ruby]

desc 'Run tests and lint'
task ci: %w[test rubocop]

task default: :test

def go_build(platform)
  target = PLATFORMS.fetch(platform)
  out_dir = File.expand_path(File.join('exe', platform))
  mkdir_p out_dir
  out = File.join(out_dir, 'finesse')

  puts "Building #{platform} -> exe/#{platform}/finesse"
  Dir.chdir(GO_SRC) do
    ENV.update('CGO_ENABLED' => '0', 'GOOS' => target[:goos], 'GOARCH' => target[:goarch])
    sh "go build -trimpath -ldflags='-s -w' -o #{out} ."
  end
end

namespace :build do
  desc 'Cross-compile Go binary for a specific platform (e.g., rake build:go[arm64-darwin])'
  task :go, [:platform] do |_t, args|
    platform = args[:platform]
    abort "Unknown platform: #{platform}. Valid: #{PLATFORMS.keys.join(', ')}" unless PLATFORMS.key?(platform)
    go_build(platform)
  end

  desc 'Compile Go binary for the current platform'
  task :local do
    require_relative 'lib/finesse/platforms'
    Rake::Task['build:go'].invoke(Finesse::Platforms.platform)
  end

  desc 'Cross-compile Go binary for all platforms'
  task :all do
    PLATFORMS.each_key do |platform|
      Rake::Task['build:go'].reenable
      Rake::Task['build:go'].invoke(platform)
    end
  end
end

desc 'Build gem (compiles Go binaries first)'
task build: 'build:all'

desc 'Release a new version (e.g., rake release_gem[0.1.0.beta1])'
task :release_gem, [:version] do |_t, args|
  version = args[:version]
  abort 'Usage: rake release_gem[VERSION]' unless version

  version_file = 'lib/finesse/version.rb'
  content = File.read(version_file)
  new_content = content.sub(/VERSION = '.*'/, "VERSION = '#{version}'")
  abort 'VERSION not found in version.rb' if content == new_content

  File.write(version_file, new_content)
  sh "git add #{version_file}"
  sh "git commit -m 'Bump version to #{version}'"
  sh "git tag v#{version}"
  sh 'git push --tags'
  sh 'git push'

  prerelease = version.match?(/[a-zA-Z]/)
  flags = prerelease ? '--prerelease' : ''
  sh "gh release create v#{version} --title 'v#{version}' --generate-notes #{flags}"
end
