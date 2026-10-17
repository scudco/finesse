# frozen_string_literal: true

require 'rails/generators'

module Finesse
  module Generators
    # rails g finesse:install
    class InstallGenerator < Rails::Generators::Base
      PUMA_PLUGIN = /^[^#\n]*plugin\s+:finesse\b/

      desc 'Adds the Finesse Puma plugin and an initializer.'

      def add_puma_plugin
        path = 'config/puma.rb'
        if !File.exist?(File.join(destination_root, path))
          say_status :warn, "#{path} not found; add `plugin :finesse` to your Puma config", :yellow
        elsif File.read(File.join(destination_root, path)).match?(PUMA_PLUGIN)
          say_status :skip, "#{path} already has plugin :finesse", :yellow
        else
          append_to_file path, "\nplugin :finesse\n"
        end
      end

      def create_initializer
        create_file 'config/initializers/finesse.rb', <<~RUBY, skip: true
          # Public URL browsers use to reach Finesse. Defaults to FINESSE_URL, or
          # http://localhost:$FINESSE_PORT (4000) when unset.
          # Rails.application.config.finesse.url = "https://sse.example.com"

          # Origins allowed to connect are set with the FINESSE_ALLOW_ORIGIN env var
          # (comma-separated, e.g. "https://example.com"). Required outside development.
        RUBY
      end

      def show_next_steps
        say <<~TEXT

          Next: replace `turbo_stream_from` with `finesse_stream_from` in your views.
          In production, set FINESSE_ALLOW_ORIGIN and config.finesse.url (or FINESSE_URL).
        TEXT
      end
    end
  end
end
