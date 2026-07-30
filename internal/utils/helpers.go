package utils

import (
	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/cache"
	"EverythingSuckz/fsb/internal/types"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// https://stackoverflow.com/a/70802740/15807350
func Contains[T comparable](s []T, e T) bool {
	for _, v := range s {
		if v == e {
			return true
		}
	}
	return false
}

// IsClientDisconnectError checks if the error is due to client disconnecting
// e.g. user seeking in video, stopping playback, or network issues on client side
func IsClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection was aborted") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "forcibly closed")
}

// telegram helper functions
// TODO: move these to a separate package if they grow too large

func GetTGMessage(ctx context.Context, client *gotgproto.Client, messageID int) (*tg.Message, error) {
	inputMessageID := tg.InputMessageClass(&tg.InputMessageID{ID: messageID})
	channel, err := GetLogChannelPeer(ctx, client.API(), client.PeerStorage)
	if err != nil {
		return nil, err
	}
	messageRequest := tg.ChannelsGetMessagesRequest{Channel: channel, ID: []tg.InputMessageClass{inputMessageID}}
	res, err := client.API().ChannelsGetMessages(ctx, &messageRequest)
	if err != nil {
		return nil, err
	}
	messages := res.(*tg.MessagesChannelMessages)
	message := messages.Messages[0]
	if _, ok := message.(*tg.Message); ok {
		return message.(*tg.Message), nil
	} else {
		return nil, fmt.Errorf("this file was deleted")
	}
}

func FileFromMedia(media tg.MessageMediaClass) (*types.File, error) {
	switch media := media.(type) {
	case *tg.MessageMediaDocument:
		document, ok := media.Document.AsNotEmpty()
		if !ok {
			return nil, fmt.Errorf("unexpected type %T", media)
		}
		var fileName string
		var duration int
		var width, height int
		for _, attribute := range document.Attributes {
			Logger.Sugar().Infof("Telegram Attribute: %T %+v", attribute, attribute)
			switch attr := attribute.(type) {
			case *tg.DocumentAttributeFilename:
				fileName = attr.FileName
			case *tg.DocumentAttributeVideo:
				duration = int(attr.Duration)
				width = attr.W
				height = attr.H
			case *tg.DocumentAttributeAudio:
				duration = int(attr.Duration)
			}
		}
		return &types.File{
			Location: document.AsInputDocumentFileLocation(),
			FileSize: document.Size,
			FileName: fileName,
			MimeType: document.MimeType,
			ID:       document.ID,
			Duration: duration,
			Width:    width,
			Height:   height,
		}, nil
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.AsNotEmpty()
		if !ok {
			return nil, fmt.Errorf("unexpected type %T", media)
		}
		sizes := photo.Sizes
		if len(sizes) == 0 {
			return nil, errors.New("photo has no sizes")
		}
		photoSize := sizes[len(sizes)-1]
		size, ok := photoSize.AsNotEmpty()
		if !ok {
			return nil, errors.New("photo size is empty")
		}
		location := new(tg.InputPhotoFileLocation)
		location.ID = photo.GetID()
		location.AccessHash = photo.GetAccessHash()
		location.FileReference = photo.GetFileReference()
		location.ThumbSize = size.GetType()
		return &types.File{
			Location: location,
			FileSize: 0, // caller should judge if this is a photo or not
			FileName: fmt.Sprintf("photo_%d.jpg", photo.GetID()),
			MimeType: "image/jpeg",
			ID:       photo.GetID(),
		}, nil
	}
	return nil, fmt.Errorf("unexpected type %T", media)
}

func FileFromMessage(ctx context.Context, client *gotgproto.Client, messageID int) (*types.File, error) {
	key := fmt.Sprintf("file:%d:%d", messageID, client.Self.ID)
	log := Logger.Named("GetMessageMedia")
	var cachedMedia types.File
	err := cache.GetCache().Get(key, &cachedMedia)
	if err == nil {
		log.Debug("Using cached media message properties", zap.Int("messageID", messageID), zap.Int64("clientID", client.Self.ID))
		return &cachedMedia, nil
	}
	log.Debug("Fetching file properties from message ID", zap.Int("messageID", messageID), zap.Int64("clientID", client.Self.ID))
	message, err := GetTGMessage(ctx, client, messageID)
	if err != nil {
		return nil, err
	}
	file, err := FileFromMedia(message.Media)
	if err != nil {
		return nil, err
	}
	file.ForwardedFrom, file.ForwardedBy = ExtractForwardInfo(ctx, client.API(), message)
	err = cache.GetCache().Set(
		key,
		file,
		3600,
	)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func ExtractForwardInfo(ctx context.Context, api *tg.Client, msg *tg.Message) (forwardedFrom string, forwardedBy string) {
	forwardedFrom = "Direct Upload"
	forwardedBy = "Unknown User"

	if msg == nil {
		return forwardedFrom, forwardedBy
	}

	// Extract channel/chat forwarded from
	fwd := msg.FwdFrom
	if fwd.FromName != "" {
		forwardedFrom = fwd.FromName
	} else if fwd.FromID != nil {
		switch p := fwd.FromID.(type) {
		case *tg.PeerChannel:
			inputChannel := &tg.InputChannel{ChannelID: p.ChannelID}
			channels, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{inputChannel})
			if err == nil && len(channels.GetChats()) > 0 {
				if ch, ok := channels.GetChats()[0].(*tg.Channel); ok {
					forwardedFrom = ch.Title
				} else {
					forwardedFrom = fmt.Sprintf("Channel #%d", p.ChannelID)
				}
			} else {
				forwardedFrom = fmt.Sprintf("Channel #%d", p.ChannelID)
			}
		case *tg.PeerUser:
			users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: p.UserID}})
			if err == nil && len(users) > 0 {
				if u, ok := users[0].(*tg.User); ok {
					name := strings.TrimSpace(u.FirstName + " " + u.LastName)
					if u.Username != "" {
						forwardedFrom = fmt.Sprintf("%s (@%s)", name, u.Username)
					} else {
						forwardedFrom = name
					}
				}
			} else {
				forwardedFrom = fmt.Sprintf("User #%d", p.UserID)
			}
		}
	} else if fwd.PostAuthor != "" {
		forwardedFrom = fwd.PostAuthor
	}

	// Extract user who forwarded/sent message
	if msg.FromID != nil {
		if peerUser, ok := msg.FromID.(*tg.PeerUser); ok {
			users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUser{UserID: peerUser.UserID}})
			if err == nil && len(users) > 0 {
				if u, ok := users[0].(*tg.User); ok {
					name := strings.TrimSpace(u.FirstName + " " + u.LastName)
					if u.Username != "" {
						forwardedBy = fmt.Sprintf("%s (@%s)", name, u.Username)
					} else {
						forwardedBy = name
					}
				}
			}
		}
	}

	return forwardedFrom, forwardedBy
}

func GetLogChannelPeer(ctx context.Context, api *tg.Client, peerStorage *storage.PeerStorage) (*tg.InputChannel, error) {
	cachedInputPeer := peerStorage.GetInputPeerById(config.ValueOf.LogChannelID)

	switch peer := cachedInputPeer.(type) {
	case *tg.InputPeerEmpty:
		break
	case *tg.InputPeerChannel:
		return &tg.InputChannel{
			ChannelID:  peer.ChannelID,
			AccessHash: peer.AccessHash,
		}, nil
	default:
		return nil, errors.New("unexpected type of input peer")
	}
	inputChannel := &tg.InputChannel{
		ChannelID: config.ValueOf.LogChannelID,
	}
	channels, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{inputChannel})
	if err != nil {
		return nil, err
	}
	if len(channels.GetChats()) == 0 {
		return nil, errors.New("no channels found")
	}
	channel, ok := channels.GetChats()[0].(*tg.Channel)
	if !ok {
		return nil, errors.New("type assertion to *tg.Channel failed")
	}
	// Bruh, I literally have to call library internal functions at this point
	peerStorage.AddPeer(channel.GetID(), channel.AccessHash, storage.TypeChannel, "")
	return channel.AsInput(), nil
}

func ForwardMessages(ctx *ext.Context, fromChatId, toChatId int64, messageID int) (*tg.Updates, error) {
	fromPeer := ctx.PeerStorage.GetInputPeerById(fromChatId)
	if fromPeer.Zero() {
		return nil, fmt.Errorf("fromChatId: %d is not a valid peer", fromChatId)
	}
	toPeer, err := GetLogChannelPeer(ctx, ctx.Raw, ctx.PeerStorage)
	if err != nil {
		return nil, err
	}
	update, err := ctx.Raw.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
		RandomID: []int64{rand.Int63()},
		FromPeer: fromPeer,
		ID:       []int{messageID},
		ToPeer:   &tg.InputPeerChannel{ChannelID: toPeer.ChannelID, AccessHash: toPeer.AccessHash},
	})
	if err != nil {
		return nil, err
	}
	return update.(*tg.Updates), nil
}
