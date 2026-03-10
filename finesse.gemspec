# frozen_string_literal: true

require_relative 'lib/finesse/version'

Gem::Specification.new do |spec|
  spec.name        = 'finesse'
  spec.version     = Finesse::VERSION
  spec.authors     = ['scudco']
  spec.summary     = 'SSE server for ActionCable in Rails'
  spec.description = "Finesse replaces ActionCable's default WebSocket transport with a lightweight " \
                     "Go binary that polls SolidCable's SQLite table and streams Turbo updates to " \
                     'browsers via Server-Sent Events.'
  spec.homepage    = 'https://github.com/scudco/finesse'
  spec.license     = 'MIT'

  spec.required_ruby_version = '>= 4.0'

  spec.files = Dir['lib/**/*', 'exe/**/*', 'LICENSE', 'README.md']
  spec.bindir = 'exe'
  spec.executables = ['finesse']
  spec.require_paths = ['lib']

  spec.add_dependency 'turbo-rails', '>= 2.0'
  spec.metadata['rubygems_mfa_required'] = 'true'
end
