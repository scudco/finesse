# frozen_string_literal: true

require 'test_helper'
require 'rails'
require 'action_view'
require 'finesse/streams_helper'

# Stand-in for turbo-rails so the test needn't boot its engine.
module Turbo
  module StreamsChannel
    def self.signed_stream_name(streamables)
      Array(streamables).join(':').then { |name| "#{name}+/=--abc+/=" }
    end
  end
end

class TestStreamsHelper < Minitest::Test
  FakeApp = Struct.new(:config)

  def setup
    @previous_app = Rails.application
    use_url 'http://localhost:4000'
    @view = ActionView::Base.empty.extend(Finesse::StreamsHelper)
  end

  def teardown
    Rails.application = @previous_app
  end

  def test_renders_turbo_stream_source_pointing_at_finesse
    html = @view.finesse_stream_from('chat')

    assert_includes html, '<turbo-stream-source src="http://localhost:4000/events?signed_stream='
    assert_includes html, '></turbo-stream-source>'
  end

  def test_escapes_token_characters_in_the_query
    html = @view.finesse_stream_from('chat')

    assert_includes html, 'signed_stream=chat%2B%2F%3D--abc%2B%2F%3D'
  end

  def test_multiple_streamables_sign_one_stream
    html = @view.finesse_stream_from('chat', :messages)

    assert_includes html, 'signed_stream=chat%3Amessages%2B'
  end

  def test_passes_extra_attributes_through
    html = @view.finesse_stream_from('chat', id: 'feed', data: { room: 1 })

    assert_includes html, 'id="feed"'
    assert_includes html, 'data-room="1"'
  end

  def test_strips_trailing_slash_from_url
    use_url 'https://sse.example.com/'

    assert_includes @view.finesse_stream_from('chat'), 'src="https://sse.example.com/events?'
  end

  def test_raises_when_streamables_are_blank
    assert_raises(ArgumentError) { @view.finesse_stream_from }
    assert_raises(ArgumentError) { @view.finesse_stream_from('', nil) }
  end

  private

  def use_url(url)
    Rails.application = FakeApp.new(Struct.new(:finesse).new(Struct.new(:url).new(url)))
  end
end
