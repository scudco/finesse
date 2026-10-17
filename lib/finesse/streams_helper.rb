# frozen_string_literal: true

module Finesse
  # View helper that points a Turbo stream subscription at the Finesse
  # server instead of ActionCable.
  module StreamsHelper
    # Same arguments as turbo-rails' `turbo_stream_from`. Renders a
    # `<turbo-stream-source>` whose `src` is Finesse's /events endpoint.
    #
    #   <%= finesse_stream_from @chat, :messages %>
    def finesse_stream_from(*streamables, **attributes)
      raise ArgumentError, "streamables can't be blank" unless streamables.any?(&:present?)

      token = Turbo::StreamsChannel.signed_stream_name(streamables)
      base = Rails.application.config.finesse.url.to_s.chomp('/')
      tag.turbo_stream_source(src: "#{base}/events?#{{ signed_stream: token }.to_query}", **attributes)
    end
  end
end
