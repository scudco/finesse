# frozen_string_literal: true

require 'rails/generators'

module Finesse
  module Generators
    # rails g finesse:systemd
    class SystemdGenerator < Rails::Generators::Base
      desc 'Writes config/finesse.service, a systemd unit that runs Finesse on its own.'

      class_option :origin, type: :string, default: 'https://example.com',
                            desc: 'Origin allowed to connect (--allow-origin)'
      class_option :user, type: :string, default: 'deploy', desc: 'User the service runs as'

      def create_unit
        create_file 'config/finesse.service', unit
      end

      def show_next_steps
        say 'Copy to /etc/systemd/system/finesse.service, then: systemctl enable --now finesse'
      end

      private

      def unit
        <<~UNIT
          [Unit]
          Description=Finesse SSE server
          After=network.target

          [Service]
          User=#{options[:user]}
          WorkingDirectory=#{destination_root}
          Environment=RAILS_ENV=production
          # Set Environment=FINESSE_SIGNING_KEY=<hex> to skip booting Rails for the key.
          ExecStart=/usr/bin/env bundle exec finesse --allow-origin #{options[:origin]}
          Restart=always

          [Install]
          WantedBy=multi-user.target
        UNIT
      end
    end
  end
end
